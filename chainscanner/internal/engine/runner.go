package engine

import (
	"context"
	"errors"
	"fmt"
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

// Step commits each contiguous range and its matching deliveries atomically.
func (s *Store) Step(ctx context.Context, n chains.Network, source chains.Source) (bool, error) {
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
	latest, err := source.Head(ctx)
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
	if prev >= head {
		return false, nil
	}
	commit := func(end int64, events []chains.Activity) error {
		if end <= prev || end > head {
			return fmt.Errorf("invalid batch end %d after %d", end, prev)
		}
		for i := range events {
			if events[i].Height <= prev || events[i].Height > end {
				return errors.New("event height outside batch")
			}
			events[i].Confirmations = latest - events[i].Height + 1
		}
		s.demandMu.Lock()
		defer s.demandMu.Unlock()
		if s.periodLocked(n.Name).active != period {
			return context.Canceled
		}
		if err := s.commit(ctx, n.Name, prev, end, events); err != nil {
			return err
		}
		prev = end
		return nil
	}
	if pool, ok := source.(interface {
		Scan(context.Context, int64, int64, func(int64, []chains.Activity) error) error
	}); ok {
		err = pool.Scan(ctx, prev+1, head, commit)
	} else {
		var end int64
		var events []chains.Activity
		end, events, err = source.Read(ctx, prev+1, head)
		if err == nil {
			err = commit(end, events)
		}
	}
	return err == nil && prev < head, err
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
			behind, err = s.Step(ctx, n, src)
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
