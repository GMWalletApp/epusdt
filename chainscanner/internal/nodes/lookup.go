package nodes

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

const strictGroupSize = 2

var errCapacity = errors.New("insufficient available transaction nodes")
var errAgreement = errors.New("transaction lookup failed")

type lookupCandidate struct {
	weight     int
	healthy    bool
	fallback   bool
	peerID     string
	primaryURL string
	cfg        chains.Network
	public     bool
	version    nodeVersion
	key        string
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
	nextSweep time.Time
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
	// Bound storage without scanning every provider while holding the lock on every reply.
	if !now.Before(l.nextSweep) {
		for key, p := range l.penalties {
			if !now.Before(p.expires) {
				delete(l.penalties, key)
			}
		}
		l.nextSweep = now.Add(time.Minute)
	}
	if err == nil || errors.Is(err, chains.ErrTransactionMissing) {
		l.penalties[id] = lookupPenalty{verified: now.Add(time.Minute), expires: now.Add(time.Minute)}
		return
	}
	p := l.penalties[id]
	if !now.Before(p.expires) {
		p = lookupPenalty{}
	}
	p.verified = time.Time{}
	p.retry = now.Add(p.streak.fail())
	p.expires = p.retry.Add(time.Hour)
	l.penalties[id] = p
}

func (r *NodeRegistry) lookupCandidates(ctx context.Context, network string, strict bool, manual ...bool) ([]lookupCandidate, error) {
	rows, err := r.List(ctx, network)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var candidates []lookupCandidate
	for _, n := range rows {
		if !n.Enabled || n.Purpose == "manual_verify" && !(strict && len(manual) > 0 && manual[0]) || n.Role == "fallback" && !strict {
			continue
		}
		if n.ReadRetryAt > now.UnixMilli() || (!n.Scannable && n.Protocol != "toncenter-v3") {
			continue
		}
		recent := n.CheckedAt != nil && now.Sub(time.UnixMilli(*n.CheckedAt)) < time.Minute
		if recent && n.Status != "ok" && n.Status != "untested" {
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
		candidates = append(candidates, lookupCandidate{
			healthy: recent && n.Status == "ok", fallback: n.Role == "fallback", weight: n.Weight,
			cfg: cfg, version: n.version(), public: isDiscoveryOrigin(n.Origin),
			key: key, peerID: peerID, primaryURL: n.URL,
		})
	}
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })

	slices.SortStableFunc(candidates, func(a, b lookupCandidate) int { return cmp.Compare(b.weight, a.weight) })
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
	reserved := maps.Clone(used)
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
		// Independent fallback providers may fill both voting slots.
		group = l.fill(idleCtx, paidFresh, paidCold, reserved, count, true)
	}
	group = append(group, l.fill(idleCtx, fresh, cold, reserved, count-len(group), false)...)
	stop()
	if count == strictGroupSize && len(group) < count {
		group = append(group, l.fill(ctx, fresh, cold, reserved, count-len(group), true)...)
		if len(group) < count {
			group = append(group, l.fill(ctx, paidFresh, paidCold, reserved, count-len(group), true)...)
		}
	}

	if len(group) == count {
		for _, lease := range group {
			used[lease.key] = true
			if lease.peerID != "" {
				used[lease.peerID] = true
			}
		}
		return group
	}
	for _, lease := range group {
		lease.release()
	}
	return nil
}

func (l *transactionLookup) fill(ctx context.Context, fresh, cold []lookupCandidate, used map[string]bool, count int, allowBusy bool) []lookupLease {
	if count <= 0 {
		return nil
	}
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

// Prefer a pair of operator-selected voters. Otherwise use LiteServer proofs
// when available, with independent v3 indexes as an alternative.
func (r *NodeRegistry) tonGroup(ctx context.Context, candidates []lookupCandidate, used map[string]bool, hash string) ([]lookupLease, transactionReader) {
	var peers, indexes, fallbackIndexes []lookupCandidate
	paidPeers := 0
	for _, c := range candidates {
		if c.peerID != "" {
			peers = append(peers, c)
			if c.fallback {
				paidPeers++
			}
		} else {
			indexes = append(indexes, c)
			if c.fallback {
				fallbackIndexes = append(fallbackIndexes, c)
			}
		}
	}
	if paidPeers < strictGroupSize && len(fallbackIndexes) >= strictGroupSize {
		// An incomplete reservation must not exclude a healthy index from the mixed pair below.
		selected := maps.Clone(used)
		if group := r.lookup.group(ctx, fallbackIndexes, selected, strictGroupSize); len(group) == strictGroupSize {
			maps.Copy(used, selected)
			return group, r.lookup.read
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
