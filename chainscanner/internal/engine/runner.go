package engine

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

// The normal start is the next observed block. An explicit lookback opts in
// to a bounded historical window on activation for clients doing recovery.
func ActivationCursor(tip int64, n chains.Network) int64 {
	if n.Lookback <= 0 {
		return tip
	}
	eligible := max(int64(-1), tip-max(n.Confirmations-1, 0))
	return max(int64(-1), eligible-n.Lookback)
}

// Step reserves confirmed work and atomically queues deliveries as each read completes.
func (s *Store) Step(ctx context.Context, n chains.Network, source chains.Source) (bool, error) {
	return s.step(ctx, n, source, false)
}

// Pump fills free provider lanes while earlier reads remain in flight. Run it
// serially inside WhileWatched with one pool owned by that watch period.
func (s *Store) Pump(ctx context.Context, n chains.Network, source chains.Source) (bool, error) {
	return s.step(ctx, n, source, true)
}

type liveSource interface {
	LiveWindow(context.Context, context.Context, time.Duration) (int64, []int64, int, error)
	AdvanceLive(context.Context, []chains.Span, chains.FinishRange) (bool, error)
}

func (s *Store) step(ctx context.Context, n chains.Network, source chains.Source, continuous bool) (bool, error) {
	lock, _ := s.workers.LoadOrStore(n.Name, new(sync.Mutex))
	worker := lock.(*sync.Mutex)
	worker.Lock()
	defer worker.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	period, _ := s.period(n.Name)
	if period == nil {
		return false, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(period, cancel)
	defer func() { stop(); cancel() }()
	if period.Err() != nil {
		return false, nil
	}
	var latest int64
	var err error
	var owned []int64
	limit := 16
	pool, rolling := source.(liveSource)
	rolling = rolling && continuous
	interval := time.Duration(n.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if rolling {
		latest, owned, limit, err = pool.LiveWindow(ctx, period, interval)
	} else {
		latest, err = source.Head(ctx)
		if pool, ok := source.(interface{ ScanWindow() int }); ok {
			limit = pool.ScanWindow()
		}
	}
	if err != nil {
		return false, err
	}
	head := latest - max(n.Confirmations-1, 0)
	if head < 0 {
		return false, nil
	}
	prev, ok, err := s.cursor(ctx, n.Name)
	if err != nil {
		return false, err
	}
	if !ok {
		prev = ActivationCursor(latest, n)
		s.demandMu.Lock()
		if s.periodLocked(n.Name).active != period {
			s.demandMu.Unlock()
			return false, nil
		}
		err = s.SeedCursor(ctx, n.Name, prev)
		s.demandMu.Unlock()
		if err != nil {
			return false, err
		}
	}
	// Publish each completed span immediately. Reservations and completions
	// both verify the watch generation while holding the subscription lock.
	s.demandMu.Lock()
	if s.periodLocked(n.Name).active != period {
		s.demandMu.Unlock()
		return false, nil
	}
	var retryDelay time.Duration
	if rolling {
		retryDelay = interval
	}
	if drain, ok := s.drains[n.Name]; ok && !drain.open {
		head = min(head, drain.end)
	}
	work, high, err := s.planAvailable(ctx, n.Name, prev, head, limit, owned, retryDelay)
	s.demandMu.Unlock()
	if err != nil || (!rolling && len(work) == 0) {
		return false, err
	}
	completed := 0
	var unavailable error
	finish := func(span chains.Span, link chains.BlockLink, events []chains.Activity, readErr error) (finishErr error) {
		s.demandMu.Lock()
		defer s.demandMu.Unlock()
		p := s.periodLocked(n.Name)
		if p.active != period {
			return context.Canceled
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// A fork rejected at commit can still be retried on another node.
		// Count the terminal range result once, after any local validation and commit.
		defer func() {
			if p.active != period || ctx.Err() != nil || errors.Is(readErr, context.Canceled) || errors.Is(finishErr, context.Canceled) {
				return
			}
			switch {
			case readErr == nil && finishErr == nil:
				p.scanSuccessCount++
			case readErr != nil || !errors.Is(finishErr, chains.ErrFork):
				p.scanFailureCount++
			}
		}()
		if readErr != nil {
			unavailable = readErr
			return s.deferGap(ctx, n.Name, span)
		}
		var sourceIDs []string
		if len(events) != 0 {
			sourceIDs = chains.SourceIDs(n)
		}
		for i := range events {
			e := &events[i]
			if len(e.ScanSources) == 0 {
				e.ScanSources = sourceIDs
			}
			if e.Height < span.First || e.Height > span.Last || (e.Network != "" && e.Network != n.Name) {
				return errors.New("event outside assigned range")
			}
			e.Confirmations = max(n.Confirmations, latest-e.Height+1)
		}
		if err := s.complete(ctx, n.Name, span, link, events); err != nil {
			return err
		}
		completed++
		return nil
	}
	if rolling {
		more, err := pool.AdvanceLive(ctx, work, finish)
		if err != nil {
			return false, err
		}
		if more {
			return true, nil
		}
		return high < head && completed > 0, unavailable
	}
	if err = scanWork(ctx, n.Name, source, work, finish); err != nil {
		return false, err
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if completed == 0 {
		return false, unavailable
	}
	// Keep catching up while fresh work is available. Failed holes alone
	// wait for the next head notification or poll, avoiding a retry spin.
	var untried bool
	if high >= head {
		if e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scan_gaps WHERE network=? AND attempted=0 AND last<=?)`, n.Name, head).Scan(&untried); e != nil {
			return false, e
		}
	}
	if high < head || untried {
		return true, nil
	}
	return false, unavailable
}

func scanWork(ctx context.Context, network string, source chains.Source, work []chains.Span, finish func(chains.Span, chains.BlockLink, []chains.Activity, error) error) error {
	if pool, ok := source.(interface {
		ScanPending(context.Context, []chains.Span, func(chains.Span, chains.BlockLink, []chains.Activity, error) error) error
	}); ok {
		return pool.ScanPending(ctx, work, finish)
	}
	for _, span := range work {
		if err := ctx.Err(); err != nil {
			return err
		}
		call, cancel := context.WithTimeout(ctx, 60*time.Second)
		part, link, events, err := chains.ReadSpan(call, network, source, span)
		cancel()
		if err == nil {
			err = finish(part, link, events, nil)
			if err == nil {
				continue
			}
			if !errors.Is(err, chains.ErrFork) {
				return err
			}
		}
		if err = finish(span, chains.BlockLink{}, nil, err); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Run(ctx context.Context, n chains.Network) {
	s.WhileWatched(ctx, n.Name, func(live context.Context) { RunWithSource(live, s, n, chains.NewSource, chains.Wakeups) })
}
func RunWithSource(ctx context.Context, s *Store, n chains.Network, open func(context.Context, chains.Network) (chains.Source, error), wakeups func(chains.Source) <-chan struct{}) {
	var src chains.Source
	defer func() {
		if src != nil {
			src.Close()
		}
	}()
	for ctx.Err() == nil {
		var err error
		if src == nil {
			src, err = open(ctx, n)
		}
		var behind bool
		if err == nil {
			behind, err = s.Pump(ctx, n, src)
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			// RPC errors may contain provider tokens in URLs.
			log.Printf("chain %s: %s", n.Name, chains.RedactError(err, n))
			if src != nil {
				src.Close()
				src = nil
			}
		}
		if err == nil && behind {
			continue
		}
		delay := time.Duration(n.IntervalSeconds) * time.Second
		if delay <= 0 {
			delay = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-wakeups(src):
		case <-time.After(delay):
		}
	}
}
