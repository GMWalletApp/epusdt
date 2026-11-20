package scanner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Source returns contiguous confirmed heights (ledger versions for Aptos).
type Source interface {
	Head(context.Context) (int64, error)
	Read(ctx context.Context, next, head int64) (end int64, events []Activity, err error)
	Close()
}
type Network struct {
	Protocol        string `json:"protocol,omitempty"`
	HistoryURL      string `json:"history_url,omitempty"`
	HistoryAPIKey   string `json:"history_api_key,omitempty"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	APIKey          string `json:"api_key,omitempty"`
	Confirmations   int64  `json:"confirmations,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	Lookback        int64  `json:"lookback,omitempty"`
	TraceCalls      bool   `json:"trace_calls,omitempty"`
}

// The normal start is the next observed block. An explicit lookback opts in
// to a bounded historical window on activation for clients doing recovery.
func activationCursor(tip int64, n Network) int64 {
	if n.Lookback <= 0 {
		return tip
	}
	eligible := max(int64(-1), tip-max(n.Confirmations-1, 0))
	return max(int64(-1), eligible-n.Lookback)
}

// Step commits the full batch and every matching delivery atomically.
func (s *Store) Step(ctx context.Context, n Network, source Source) (bool, error) {
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
		prev = activationCursor(latest, n)
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
	end, events, err := source.Read(ctx, prev+1, head)
	if err != nil {
		return false, err
	}
	if end <= prev || end > head {
		return false, fmt.Errorf("invalid batch end %d after %d", end, prev)
	}
	for i := range events {
		if events[i].Height <= prev || events[i].Height > end {
			return false, errors.New("event height outside batch")
		}
		events[i].Confirmations = latest - events[i].Height + 1
	}
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	if s.periodLocked(n.Name).active != period {
		return false, nil
	}
	if err = s.commit(ctx, n.Name, prev, end, events); err != nil {
		return false, err
	}
	return end < head, nil
}
func (s *Store) Run(ctx context.Context, n Network) {
	s.WhileWatched(ctx, n.Name, func(live context.Context) { s.run(live, n, NewSource) })
}
func (s *Store) run(ctx context.Context, n Network, open func(context.Context, Network) (Source, error)) {
	var src Source
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
			log.Printf("chain %s: %s", n.Name, safeError(err, n))
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
		case <-Wakeups(src):
		case <-time.After(delay):
		}
	}
}
