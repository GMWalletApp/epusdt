package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

var errCapacity = errors.New("insufficient available transaction nodes")
var errAgreement = errors.New("transaction lookup failed")

type lookupCandidate struct {
	healthy bool
	cfg     chains.Network
	public  bool
	version nodeVersion
	key     string
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

func (r *NodeRegistry) lookupCandidates(ctx context.Context, network string) ([]lookupCandidate, error) {
	rows, err := r.List(ctx, network)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var candidates []lookupCandidate
	for _, n := range rows {
		if n.Origin == "fallback" || (!n.Scannable && n.Protocol != "toncenter-v3") || n.ReadRetryAt > now.UnixMilli() {
			continue
		}
		if n.Status != "ok" && n.Status != "untested" && n.CheckedAt != nil && now.Sub(time.UnixMilli(*n.CheckedAt)) < time.Minute {
			continue
		}
		cfg, ok := chains.LookupRoute(r.network(n, chains.Network{}))
		if !ok {
			continue
		}
		candidates = append(candidates, lookupCandidate{healthy: n.Status == "ok" && n.CheckedAt != nil && now.Sub(time.UnixMilli(*n.CheckedAt)) < time.Minute, cfg: cfg, public: n.Origin == "chainlist" || n.Origin == "openchainbench", version: n.version(), key: providerKey(cfg)})
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
		if used[c.key] || !l.ready(c.version, time.Now()) {
			continue
		}
		if release, ok := l.lanes.tryLookup(c.key, allowBusy); ok {
			used[c.key] = true
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
	var fresh, cold []lookupCandidate
	l.mu.Lock()
	for _, c := range candidates {
		if c.healthy || time.Now().Before(l.penalties[c.version].verified) {
			fresh = append(fresh, c)
		} else {
			cold = append(cold, c)
		}
	}
	l.mu.Unlock()
	idleWindow := 4 * time.Second
	if count == 3 {
		idleWindow = 2 * time.Second
	}
	idleCtx, stop := context.WithTimeout(ctx, idleWindow)
	group := l.fill(idleCtx, fresh, cold, used, count, false)
	stop()
	if count == 3 && len(group) < count {
		group = append(group, l.fill(ctx, fresh, cold, used, count-len(group), true)...)
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

func (l *transactionLookup) round(ctx context.Context, group []lookupLease, hash string) (json.RawMessage, error) {
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
			v, err := l.read(ctx, lease.cfg, hash, lease.public)
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
				count, rounds = 3, 2
			}
			used := map[string]bool{}
			for range rounds {
				if ctx.Err() != nil {
					return
				}
				nodes, err := r.lookupCandidates(ctx, n)
				if err != nil {
					http.Error(w, "node inventory unavailable", 503)
					return
				}
				group := r.lookup.group(ctx, nodes, used, count)
				if len(group) == 0 {
					http.Error(w, errCapacity.Error(), 503)
					return
				}
				v, err := r.lookup.round(ctx, group, hash)
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
			http.Error(w, errAgreement.Error(), 502)
		}
	}
	mux.HandleFunc("GET /v1/transactions/{network}/{hash}", query(false))
	mux.HandleFunc("GET /v1/transactions/{network}/{hash}/strict", query(true))
	return mux
}
