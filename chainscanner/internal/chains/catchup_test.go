package chains

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCatchupReadsBothPartsAndKeepsOnlyContiguousEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		firstEnd   int64
		failed     bool
		end        int64
		wantEvents int
	}{
		{"complete", 109, false, 119, 2},
		{"truncated", 104, false, 104, 1},
		{"rejected", 109, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var p pairedReads
			second := make(chan struct{})
			end, events, err := p.read(ctx, 100, 110, 119, func(ctx context.Context, start, end int64) (int64, []Activity, error) {
				if start == 110 {
					close(second)
					if tc.failed {
						return 0, nil, ErrRateLimited
					}
				} else {
					select {
					case <-second:
					case <-ctx.Done():
						return 0, nil, ctx.Err()
					}
					end = tc.firstEnd
				}
				return end, []Activity{{Height: start}}, nil
			})
			if errors.Is(err, ErrRateLimited) != tc.failed || end != tc.end || len(events) != tc.wantEvents {
				t.Fatal(end, events, err)
			}
			for i, event := range events {
				if event.Height != int64(100+i*10) {
					t.Fatal(events)
				}
			}
			if p.enabled() != (tc.name == "complete") {
				t.Fatal("incorrect parallel-read cooldown")
			}
		})
	}
}
func TestRPCNodeWaitHonorsCancellation(t *testing.T) {
	budget := requestBudget{gate: make(chan struct{}, 1), spacing: time.Hour}
	if err := budget.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := budget.next
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := budget.wait(ctx); !errors.Is(err, context.Canceled) || budget.next != next {
		t.Fatal(err, budget.next)
	}
}
