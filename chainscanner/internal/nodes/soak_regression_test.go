package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
	"github.com/GMWalletApp/ChainScanner/internal/testutil"
)

func TestTailRangeUsesIdlePeerAtDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p := NewSourcePool(ctx, "tron")
		defer p.Close()
		c := []chains.Network{{Name: "tron", URL: "http://a.test"}, {Name: "tron", URL: "http://b.test"}}
		if err := p.SetNodes(c); err != nil {
			t.Fatal(err)
		}
		var failovers atomic.Int32
		p.open = func(_ context.Context, n chains.Network) (chains.Source, error) {
			return &mockLane{head: 500, read: func(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
				if n.URL == c[0].URL {
					<-ctx.Done()
					return 0, nil, ctx.Err()
				}
				failovers.Add(1)
				return b, nil, nil
			}}, nil
		}
		start := time.Now()
		done := make(chan error, 1)
		go func() {
			done <- p.ScanPending(ctx, []chains.Span{{First: 500, Last: 500}}, func(_ chains.Span, _ chains.BlockLink, _ []chains.Activity, e error) error { return e })
		}()
		time.Sleep(slowGapDelay - time.Second)
		synctest.Wait()
		if failovers.Load() != 0 {
			t.Fatal("reassigned before the deadline")
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != slowGapDelay || failovers.Load() != 1 {
			t.Fatalf("handoff after %v; standby reads=%d", elapsed, failovers.Load())
		}

	})
}

type liveHead struct {
	height func() int64
	read   func(int64, int64) (int64, []chains.Activity, error)
}

func (n *liveHead) Head(context.Context) (int64, error) { return n.height(), nil }
func (n *liveHead) Read(_ context.Context, a, b int64) (int64, []chains.Activity, error) {
	return n.read(a, b)
}
func (*liveHead) Close() {}

func TestLaggingPeerStillServesHistoricalRanges(t *testing.T) {
	ctx := context.Background()
	p := NewSourcePool(ctx, "tron")
	defer p.Close()
	configs := []chains.Network{{Name: "tron", URL: "http://a.test"}, {Name: "tron", URL: "http://b.test"}}
	if err := p.SetNodes(configs); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	var tip atomic.Int64
	tip.Store(500)
	p.open = func(_ context.Context, n chains.Network) (chains.Source, error) {
		height := func() int64 {
			if n.URL == configs[0].URL {
				return tip.Load() - 1
			}
			return tip.Load()
		}
		return &liveHead{height: height, read: func(a, b int64) (int64, []chains.Activity, error) {
			if b > height() {
				return 0, nil, errors.New("unexpected read above node head")
			}
			if n.URL == configs[0].URL {
				reads.Add(1)
			}
			return b, nil, nil
		}}, nil
	}
	// Start by showing the slower peer actually serves historical work.
	finish := func(_ chains.Span, _ chains.BlockLink, _ []chains.Activity, e error) error { return e }
	if err := p.ScanPending(ctx, []chains.Span{{First: 498, Last: 498}}, finish); err != nil {
		t.Fatal(err)
	}
	// Every newer span is resolved by the other peer. Offer the slower peer a
	// height just ahead of its tip in three separate scheduling rounds.
	if err := p.ScanPending(ctx, []chains.Span{{First: 499, Last: 499}}, finish); err != nil {
		t.Fatal(err)
	}
	for h := int64(500); h <= 502; h++ {
		tip.Store(h)
		if err := p.ScanPending(ctx, []chains.Span{{First: h, Last: h}}, finish); err != nil {
			t.Fatal(err)
		}
	}
	if !p.nodes[0].retry.IsZero() || p.nodes[0].readFailures != (failureStreak{}) {
		t.Fatalf("lag penalized the peer: %+v", p.nodes[0].readFailures)
	}
	p.next = 0
	before := reads.Load()
	if err := p.ScanPending(ctx, []chains.Span{{First: 498, Last: 498}}, finish); err != nil || reads.Load() != before+1 {
		t.Fatal("historical repair did not use healthy peer", err)
	}

}

func TestSolanaStepReusesFinalizedSlotList(t *testing.T) {
	ctx := context.Background()
	var ranges, blocks atomic.Int32
	server := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "getGenesisHash":
			return "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d", nil
		case "getSlot":
			return 16, nil
		case "getBlocks":
			ranges.Add(1)
			var start, end int
			_ = json.Unmarshal(r.Params[0], &start)
			_ = json.Unmarshal(r.Params[1], &end)
			var heights []int
			for h := start; h <= end; h++ {
				heights = append(heights, h)
			}
			return heights, nil
		case "getBlock":
			blocks.Add(1)
			return map[string]any{"blockhash": "hash", "blockTime": 1, "transactions": []any{}}, nil
		default:
			return nil, fmt.Errorf("unexpected %s", r.Method)
		}
	})
	defer server.Close()
	cfg := chains.Network{Name: "solana", URL: server.URL}
	store, e := engine.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if e = store.SeedCursor(ctx, "solana", 0); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Subscribe(ctx, "test", []feed.Watch{{Network: "solana", Address: "11111111111111111111111111111111"}}); e != nil {
		t.Fatal(e)
	}
	pool := NewSourcePool(ctx, "solana")
	defer pool.Close()
	if e = pool.SetNodes([]chains.Network{cfg}); e != nil {
		t.Fatal(e)
	}
	for {
		behind, e := store.Step(ctx, cfg, pool)
		if e != nil {
			t.Fatal(e)
		}
		if !behind {
			break
		}
	}
	if h, e := store.Height(ctx, "solana"); e != nil || h != 16 {
		t.Fatal(h, e)
	}
	if ranges.Load() != 1 || blocks.Load() != 16 {
		t.Fatalf("got getBlocks=%d getBlock=%d, want 1 and 16", ranges.Load(), blocks.Load())
	}
}
