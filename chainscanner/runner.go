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
	Name            string `json:"name"`
	URL             string `json:"url"`
	APIKey          string `json:"api_key,omitempty"`
	Confirmations   int64  `json:"confirmations,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	Lookback        int64  `json:"lookback,omitempty"`
	TraceCalls      bool   `json:"trace_calls,omitempty"`
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
		prev = max(int64(-1), head-max(n.Lookback, 1))
		if err = s.SeedCursor(ctx, n.Name, prev); err != nil {
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
	if err = s.commit(ctx, n.Name, prev, end, events); err != nil {
		return false, err
	}
	return end < head, nil
}
func (s *Store) Run(ctx context.Context, n Network) {
	var src Source
	defer func() {
		if src != nil {
			src.Close()
		}
	}()
	for ctx.Err() == nil {
		var err error
		if src == nil {
			src, err = NewSource(ctx, n)
		}
		var behind bool
		if err == nil {
			behind, err = s.Step(ctx, n, src)
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
