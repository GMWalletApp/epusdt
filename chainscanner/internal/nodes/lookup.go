package nodes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

const strictGroupSize = 2

var errCapacity = errors.New("insufficient available transaction nodes")
var errAgreement = errors.New("transaction lookup failed")

type lookupCandidate struct {
	healthy  bool
	fallback bool
	peerID   string
	cfg      chains.Network
	public   bool
	version  nodeVersion
	key      string
}
type lookupPenalty struct {
	streak                   failureStreak
	retry, expires, verified time.Time
}
type transactionLookup struct {
	reserve   sync.Mutex
	lanes     providerLanes
	mu        sync.Mutex
	penalties map[nodeVersion]lookupPenalty
	probe     func(context.Context, chains.Network, bool) error
	read      func(context.Context, chains.Network, string, bool) (json.RawMessage, error)
}

func (l *transactionLookup) ready(id nodeVersion, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !now.Before(l.penalties[id].retry)
}
func (l *transactionLookup) record(id nodeVersion, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.penalties == nil {
		l.penalties = map[nodeVersion]lookupPenalty{}
	}
	now := time.Now()
	for key, p := range l.penalties {
		if now.After(p.expires) {
			delete(l.penalties, key)
		}
	}
	if err == nil || errors.Is(err, chains.ErrTransactionMissing) {
		l.penalties[id] = lookupPenalty{verified: now.Add(time.Minute), expires: now.Add(time.Minute)}
		return
	}
	p := l.penalties[id]
	p.verified = time.Time{}
	p.retry = now.Add(p.streak.fail())
	p.expires = p.retry.Add(time.Hour)
	l.penalties[id] = p
}

func (r *NodeRegistry) lookupCandidates(ctx context.Context, network string, strict bool) ([]lookupCandidate, error) {
	rows, err := r.List(ctx, network)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var candidates []lookupCandidate
	for _, n := range rows {
		if n.Origin == "fallback" && !strict || (!n.Scannable && n.Protocol != "toncenter-v3") || n.ReadRetryAt > now.UnixMilli() {
			continue
		}
		if n.Status != "ok" && n.Status != "untested" && n.CheckedAt != nil && now.Sub(time.UnixMilli(*n.CheckedAt)) < time.Minute {
			continue
		}
		cfg, ok := chains.LookupRoute(r.network(n, chains.Network{}))
		host, peerID, peer := chains.TONVoter(cfg)
		if !ok && !(strict && peer) {
			continue
		}
		key := providerKey(cfg)
		if peer {
			key = "ton|" + host
		}
		candidates = append(candidates, lookupCandidate{healthy: n.Status == "ok" && n.CheckedAt != nil && now.Sub(time.UnixMilli(*n.CheckedAt)) < time.Minute, cfg: cfg, public: isDiscoveryOrigin(n.Origin), version: n.version(), key: key, peerID: peerID, fallback: n.Origin == "fallback"})
	}
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })

	return candidates, nil
}

type lookupLease struct {
	lookupCandidate
	release func()
}

func (l *transactionLookup) take(candidates []lookupCandidate, used map[string]bool, count int, allowBusy bool) []lookupLease {
	l.reserve.Lock()
	defer l.reserve.Unlock()
	var group []lookupLease
	for _, c := range candidates {
		if used[c.key] || c.peerID != "" && used[c.peerID] || !l.ready(c.version, time.Now()) {
			continue
		}
		if release, ok := l.lanes.tryLookup(c.key, allowBusy); ok {
			used[c.key] = true
			if c.peerID != "" {
				used[c.peerID] = true
			}
			group = append(group, lookupLease{c, release})
			if len(group) == count {
				return group
			}
		}
	}
	return group
}

func (l *transactionLookup) group(ctx context.Context, candidates []lookupCandidate, used map[string]bool, count int) []lookupLease {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var fresh, cold, paidFresh, paidCold []lookupCandidate
	l.mu.Lock()
	for _, c := range candidates {
		healthy := c.healthy || time.Now().Before(l.penalties[c.version].verified)
		if c.fallback {
			if healthy {
				paidFresh = append(paidFresh, c)
			} else {
				paidCold = append(paidCold, c)
			}
			continue
		}
		if healthy {
			fresh = append(fresh, c)
		} else {
			cold = append(cold, c)
		}
	}
	l.mu.Unlock()
	idleWindow := 4 * time.Second
	if count == strictGroupSize {
		idleWindow = 2 * time.Second
	}
	idleCtx, stop := context.WithTimeout(ctx, idleWindow)
	var group []lookupLease
	if count == strictGroupSize && len(paidFresh)+len(paidCold) > 0 {
		// Reserve at most one preferred slot before admitting general providers.
		group = l.fill(idleCtx, paidFresh, paidCold, used, 1, true)
	}
	group = append(group, l.fill(idleCtx, fresh, cold, used, count-len(group), false)...)
	stop()
	if count == strictGroupSize && len(group) < count {
		group = append(group, l.fill(ctx, fresh, cold, used, count-len(group), true)...)
		if len(group) < count {
			group = append(group, l.fill(ctx, paidFresh, paidCold, used, count-len(group), true)...)
		}
	}

	if len(group) == count {
		return group
	}
	for _, lease := range group {
		lease.release()
	}
	return nil
}

func (l *transactionLookup) fill(ctx context.Context, fresh, cold []lookupCandidate, used map[string]bool, count int, allowBusy bool) []lookupLease {
	group := l.take(fresh, used, count, allowBusy)
	for len(group) < count && ctx.Err() == nil {
		batch := l.take(cold, used, count-len(group), allowBusy)
		if len(batch) == 0 {
			break
		}
		var wg sync.WaitGroup
		ok := make([]bool, len(batch))
		for i, lease := range batch {
			wg.Go(func() {
				attempt, stop := context.WithTimeout(ctx, 2*time.Second)
				defer stop()
				err := l.probe(attempt, lease.cfg, lease.public)
				if ctx.Err() == nil && err != nil {
					l.record(lease.version, err)
				}
				ok[i] = err == nil
			})
		}
		wg.Wait()
		for i, lease := range batch {
			if ok[i] {
				group = append(group, lease)
			} else {
				lease.release()
			}
		}
	}
	return group
}

type transactionReader func(context.Context, chains.Network, string, bool) (json.RawMessage, error)

func (l *transactionLookup) round(ctx context.Context, group []lookupLease, hash string, read transactionReader) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	type result struct {
		value json.RawMessage
		err   error
	}
	results := make([]result, len(group))
	var wg sync.WaitGroup
	for i, lease := range group {
		wg.Go(func() {
			defer lease.release()
			v, err := read(ctx, lease.cfg, hash, lease.public)
			if ctx.Err() == nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				l.record(lease.version, err)
			}
			results[i] = result{v, err}
		})
	}
	wg.Wait()
	if len(results) == 1 {
		return results[0].value, results[0].err
	}
	for _, r := range results {
		if r.err != nil || len(r.value) == 0 || !bytes.Equal(r.value, results[0].value) {
			return nil, errAgreement
		}
	}
	return results[0].value, nil
}

func (r *NodeRegistry) transactionHandler(configured map[string]chains.Network) http.Handler {
	mux := http.NewServeMux()
	query := func(strict bool) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			n := req.PathValue("network")
			if _, ok := configured[n]; !ok {
				http.Error(w, "network is not configured", 400)
				return
			}
			hash, err := chains.TransactionHash(n, req.PathValue("hash"))
			if err != nil {
				http.Error(w, "invalid transaction hash", 400)
				return
			}
			ctx := req.Context()
			count, rounds := 1, 1
			if strict {
				count, rounds = strictGroupSize, 2
			}
			single := strict && req.URL.Query().Get("rounds") == "1"
			if strict && req.URL.Query().Has("rounds") && !single {
				http.Error(w, "rounds must be 1 when supplied", 400)
				return
			}
			omitted := map[string]bool{}
			if single {
				rounds = 1
				if prior := req.Header.Get("X-ChainScanner-Exclude"); prior != "" {
					tokens := strings.Split(prior, ",")
					if len(tokens) > 4 {
						http.Error(w, "invalid previous pair", 400)
						return
					}
					for _, v := range tokens {
						b, e := hex.DecodeString(v)
						if e != nil || len(b) != sha256.Size {
							http.Error(w, "invalid previous pair", 400)
							return
						}
						omitted[v] = true
					}
				}
			}
			if strict {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(45 * time.Second))
			}
			used := map[string]bool{}
			lastStatus := http.StatusBadGateway

			for attempt := 1; attempt <= rounds; attempt++ {
				if ctx.Err() != nil {
					return
				}
				if strict {
					w.Header().Set("X-ChainScanner-Strict-Rounds", strconv.Itoa(attempt))
				}
				candidates, err := r.lookupCandidates(ctx, n, strict)
				if err != nil {
					lastStatus = http.StatusServiceUnavailable
					continue
				}
				if len(omitted) > 0 {
					for _, c := range candidates {
						if omitted[voterToken(c.key)] || c.peerID != "" && omitted[voterToken(c.peerID)] {
							used[c.key] = true
						}
					}
				}
				var group []lookupLease
				read := transactionReader(r.lookup.read)
				if strict && n == "ton" {
					group, read = r.tonGroup(ctx, candidates, used, hash)
				} else {
					group = r.lookup.group(ctx, candidates, used, count)
				}
				if len(group) == 0 {
					lastStatus = http.StatusServiceUnavailable
					continue
				}
				if single {
					var tokens []string
					for _, c := range group {
						tokens = append(tokens, voterToken(c.key))
						if c.peerID != "" {
							tokens = append(tokens, voterToken(c.peerID))
						}
					}
					w.Header().Set("X-ChainScanner-Pair", strings.Join(tokens, ","))
				}
				v, err := r.lookup.round(ctx, group, hash, read)
				lastStatus = http.StatusBadGateway
				if err == nil {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(struct {
						Network string          `json:"network"`
						Hash    string          `json:"hash"`
						Result  json.RawMessage `json:"result"`
					}{n, hash, v})
					return
				}
				if !strict && errors.Is(err, chains.ErrTransactionMissing) {
					http.Error(w, "transaction not found", 404)
					return
				}
			}
			if strict && !single {
				w.Header().Set("X-ChainScanner-Manual-Review", "required")
			}
			if lastStatus == http.StatusServiceUnavailable {
				http.Error(w, errCapacity.Error(), lastStatus)
			} else {
				http.Error(w, errAgreement.Error(), lastStatus)
			}
		}
	}
	mux.HandleFunc("GET /v1/transactions/{network}/{hash}", query(false))
	mux.HandleFunc("GET /v1/transactions/{network}/{hash}/strict", query(true))
	return mux
}

// Prefer a proven LiteServer pair. Two independent v3 indexes can still
// serve strict lookups when the deployment has no available proof pair.
func (r *NodeRegistry) tonGroup(ctx context.Context, candidates []lookupCandidate, used map[string]bool, hash string) ([]lookupLease, transactionReader) {
	var peers, indexes []lookupCandidate
	for _, c := range candidates {
		if c.peerID != "" {
			peers = append(peers, c)
		} else {
			indexes = append(indexes, c)
		}
	}
	if len(peers) >= 2 {
		var location *chains.TONLocation
		locate, stop := context.WithTimeout(ctx, 3*time.Second)
		for _, idx := range indexes {
			if !r.lookup.ready(idx.version, time.Now()) {
				continue
			}
			hint, err := chains.LocateTON(locate, idx.cfg, hash)
			if err == nil {
				location = &hint
				break
			}
			if locate.Err() != nil {
				break
			}
		}
		stop()
		if hint := location; hint != nil {
			g := r.lookup.group(ctx, peers, used, strictGroupSize)
			if len(g) > 0 {
				return g, func(ctx context.Context, n chains.Network, h string, _ bool) (json.RawMessage, error) {
					return chains.VerifyTON(ctx, n, h, *hint)
				}
			}
		}
	}
	return r.lookup.group(ctx, indexes, used, strictGroupSize), r.lookup.read
}

func voterToken(key string) string { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }
