package nodes

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

var errCapacity = errors.New("insufficient available transaction nodes")
var errLookup = errors.New("transaction lookup failed")

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
	if err == nil || errors.Is(err, chains.ErrTransactionMissing) || errors.Is(err, chains.ErrTransactionPending) {
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

func (r *NodeRegistry) lookupCandidates(ctx context.Context, network string, manual bool) ([]lookupCandidate, error) {
	rows, err := r.List(ctx, network)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var candidates []lookupCandidate
	for _, n := range rows {
		if !n.Enabled || n.Purpose == "manual_verify" && !manual {
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
		if !ok && !peer {
			continue
		}
		key := chains.ProviderKey(cfg)
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

// Keep healthy nodes first within each role..
func (l *transactionLookup) prioritize(candidates []lookupCandidate) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	priority := func(c lookupCandidate) int {
		p := 0
		if c.fallback {
			p += 2
		}
		if c.healthy || now.Before(l.penalties[c.version].verified) {
			p++
		}
		return p
	}
	slices.SortStableFunc(candidates, func(a, b lookupCandidate) int {
		return cmp.Compare(priority(b), priority(a))
	})
}

func (r *NodeRegistry) readCandidate(ctx context.Context, c lookupCandidate, candidates []lookupCandidate, hash string) (json.RawMessage, error) {
	r.lookup.mu.Lock()
	healthy := c.healthy || time.Now().Before(r.lookup.penalties[c.version].verified)
	r.lookup.mu.Unlock()
	if !healthy {
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := r.lookup.probe(probe, c.cfg, c.public)
		cancel()
		if err != nil {
			return nil, errCapacity
		}
	}
	read := transactionReader(r.lookup.read)
	if c.peerID != "" {
		read = r.tonProofReader(ctx, candidates, hash)
		if read == nil {
			return nil, errCapacity
		}
	}
	raw, err := read(ctx, c.cfg, hash, c.public)
	if err != nil {
		return raw, err
	}
	if _, err := chains.TransactionEvidence(c.cfg, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

type transactionReader func(context.Context, chains.Network, string, bool) (json.RawMessage, error)

// The index supplies a location hint; only the selected LiteServer supplies the proof.
func (r *NodeRegistry) tonProofReader(ctx context.Context, candidates []lookupCandidate, hash string) transactionReader {
	locate, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	for _, idx := range candidates {
		if locate.Err() != nil {
			break
		}
		if idx.cfg.Protocol != "toncenter-v3" || !r.lookup.ready(idx.version, time.Now()) {
			continue
		}
		hint, err := chains.LocateTON(locate, idx.cfg, hash)
		if err == nil {
			return func(ctx context.Context, n chains.Network, h string, _ bool) (json.RawMessage, error) {
				return chains.VerifyTON(ctx, n, h, hint)
			}
		}
	}
	return nil
}
