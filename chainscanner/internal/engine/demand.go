package engine

import (
	"context"
	"errors"
	"time"
)

// Each uninterrupted period with at least one watch owns its RPC connections.
// Canceling the period also invalidates a late read from that generation.
type demandPeriod struct {
	started          time.Time
	heads            headHistory
	scanSuccessCount uint64
	scanFailureCount uint64
	active           context.Context
	stop             context.CancelFunc
	changed          chan struct{}
}

func (s *Store) periodLocked(network string) *demandPeriod {
	p := s.periods[network]
	if p == nil {
		p = &demandPeriod{changed: make(chan struct{})}
		s.periods[network] = p
	}
	return p
}
func (s *Store) setDemandLocked(network string, active bool) {
	p := s.periodLocked(network)
	if (p.active != nil) == active {
		return
	}
	p.heads = headHistory{}
	p.scanSuccessCount, p.scanFailureCount = 0, 0
	if active {
		p.started = time.Now()
		p.active, p.stop = context.WithCancel(context.Background())
	} else {
		p.stop()
		p.active, p.stop = nil, nil
	}
	close(p.changed)
	p.changed = make(chan struct{})
}
func (s *Store) period(network string) (context.Context, <-chan struct{}) {
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	p := s.periodLocked(network)
	return p.active, p.changed
}
func (s *Store) Watching(network string) bool { p, _ := s.period(network); return p != nil }

// WhileWatched owns a separate lifetime for each activation of one named chain.
// The callback must return when its context is canceled and close its source.
func (s *Store) WhileWatched(ctx context.Context, network string, run func(context.Context)) {
	for ctx.Err() == nil {
		active, changed := s.period(network)
		if active != nil {
			live, cancel := context.WithCancel(active)
			stop := context.AfterFunc(ctx, cancel)
			if ctx.Err() == nil && live.Err() == nil {
				run(live)
			}
			stop()
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}

type boundary struct {
	period <-chan struct{}
	height int64
}

var ErrNetworkNotConfigured = errors.New("network has no configured node")
