package nodes

import (
	"context"
	"errors"
	"time"
)

type nodeVersion struct {
	id, generation int64
	origin         string
}

func (n Node) version() nodeVersion { return nodeVersion{n.ID, n.generation, n.Origin} }

type readFailure struct {
	reason string
	retry  time.Time
}

// measured belongs to the endpoint's current RPC worker; accepted is only
// accessed by the scheduler after that worker returns.
type headSample struct {
	start   time.Time
	latency time.Duration
	height  int64
	err     error
}

func (p *SourcePool) acceptHead(n *endpoint) {
	h := n.measured
	if h.start.IsZero() {
		return
	}
	n.measured = headSample{}
	n.accepted = h
	ok := h.err == nil
	if ok {
		n.headFailures = failureStreak{}
	}
	if p.observe != nil && (n.published.IsZero() || n.publishedOK != ok || h.start.Sub(n.published) >= 5*time.Second) {
		n.published, n.publishedOK = h.start, ok
		p.observe(n.cfg, h)
	}
}

// Treat a slower peer as evidence about that peer. A higher observation
// expires after two poll intervals so stale tips cannot pin the target.
func (p *SourcePool) bestRecentTip(fallback int64, interval time.Duration) int64 {
	floor := p.now().Add(-2 * interval)
	for _, n := range p.nodes {
		if h := n.accepted; h.err == nil && h.start.After(floor) {
			fallback = max(fallback, h.height)
		}
	}
	return fallback
}

func (r *NodeRegistry) saveHead(ctx context.Context, n Node, h headSample) error {
	state := "ok"
	var height any = h.height
	if h.err != nil || h.height < 0 {
		height = nil
		state = "unavailable"
		if errors.Is(h.err, context.DeadlineExceeded) {
			state = "timeout"
		}
	}
	_, err := r.db.ExecContext(ctx, `UPDATE rpc_nodes SET status=?,height=?,checked_at=?,latency=? WHERE id=? AND removed=0 AND generation=? AND (checked_at IS NULL OR checked_at<=?)`, state, height, h.start.UnixMilli(), h.latency.Milliseconds(), n.ID, n.generation, h.start.UnixMilli())
	return err
}
