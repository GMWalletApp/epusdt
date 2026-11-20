package nodes

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

type movingSource struct {
	tip  *atomic.Int64
	read func(context.Context, int64, int64) (int64, []chains.Activity, error)
}

func (s *movingSource) Head(context.Context) (int64, error) { return s.tip.Load(), nil }
func (s *movingSource) Read(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
	return s.read(ctx, a, b)
}
func (*movingSource) Close() {}

func TestLivePumpDeliversNewHeadsWhilePreviousTailIsStillReading(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const address = "0x0000000000000000000000000000000000000001"
	if err = db.SeedCursor(ctx, "base", 99); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Subscribe(ctx, "pay", []feed.Watch{{Network: "base", Address: address}}); err != nil {
		t.Fatal(err)
	}
	cfg := []chains.Network{{Name: "base", URL: "http://slow.test", Confirmations: 4, IntervalSeconds: 1}, {Name: "base", URL: "http://fast.test", Confirmations: 4, IntervalSeconds: 1}}
	p := NewSourcePool(ctx, "base")
	defer p.Close()
	if err = p.SetNodes(cfg); err != nil {
		t.Fatal(err)
	}
	var slowTip, fastTip atomic.Int64
	slowTip.Store(103)
	fastTip.Store(103)
	entered := make(chan struct{})
	release := make(chan struct{})
	var invalid, slowReads atomic.Int64
	p.open = func(_ context.Context, n chains.Network) (chains.Source, error) {
		tip := &fastTip
		if n == cfg[0] {
			tip = &slowTip
		}
		return &movingSource{tip: tip, read: func(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
			if b+3 > tip.Load() {
				invalid.Add(1)
				return 0, nil, errors.New("range lacks four confirmations")
			}
			if n == cfg[0] && a == 100 {
				if slowReads.Add(1) == 1 {
					close(entered)
				}
				select {
				case <-ctx.Done():
					return 0, nil, ctx.Err()
				case <-release:
				}
			}
			return b, []chains.Activity{{Height: b, TxID: time.Unix(b, 0).String(), Addresses: []string{address}}}, nil
		}}, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			more, e := db.Pump(ctx, cfg[0], p)
			if e != nil && ctx.Err() == nil {
				t.Error(e)
				return
			}
			if !more {
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	fastTip.Store(107)
	seen := map[int64]bool{}
	for len(seen) < 4 {
		d, err := db.PendingNetwork(ctx, "pay", "base", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if d == nil {
			continue
		}
		h := d.Activity.Height
		if h < 101 || h > 104 || seen[h] || d.Activity.Confirmations < 4 {
			t.Fatal("bad early delivery", d)
		}
		seen[h] = true
		if err = db.Ack(ctx, "pay", d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if h, e := db.Height(ctx, "base"); e != nil || h != 99 {
		t.Fatal("crossed the open gap", h, e)
	}
	close(release)
	d, e := db.PendingNetwork(ctx, "pay", "base", time.Second)
	if e != nil || d == nil || d.Activity.Height != 100 {
		t.Fatal("missing old block", d, e)
	}
	if h, e := db.Height(ctx, "base"); e != nil || h != 104 || slowReads.Load() != 1 || invalid.Load() != 0 {
		t.Fatal(h, e, slowReads.Load(), invalid.Load())
	}
}

func TestEligibleRangeRequiresFreshHeadAtItsLastBlock(t *testing.T) {
	now := time.Now()
	n := endpoint{cfg: chains.Network{Confirmations: 4}}
	if n.covers(103, now, time.Second) {
		t.Fatal("untested node eligible")
	}
	n.accepted = headSample{start: now, height: 105}
	if !n.covers(102, now, time.Second) || n.covers(103, now, time.Second) {
		t.Fatal("wrong four-confirmation boundary")
	}
	n.accepted.height = 106
	if !n.covers(103, now, time.Second) || n.covers(103, now.Add(3*time.Second), time.Second) {
		t.Fatal("incorrect freshness or inclusive boundary")
	}
	n.accepted.err = errors.New("offline")
	if n.covers(103, now, time.Second) {
		t.Fatal("failed check eligible")
	}
}

func TestSuccessfulHeadDoesNotEraseReadFailures(t *testing.T) {
	p := NewSourcePool(context.Background(), "aptos")
	n := &endpoint{readFailures: failureStreak{attempts: 2}}
	samples := 0
	p.observe = func(_ chains.Network, h headSample) {
		samples++
		if h.height != 105 {
			t.Fatal(h.height)
		}
	}
	n.measured = headSample{start: time.Now(), height: 105}
	p.record(n, false, nil)
	if samples != 1 || n.accepted.height != 105 || n.readFailures.attempts != 2 {
		t.Fatal(samples, n.accepted, n.readFailures)
	}
}

func TestLiveQueueRecoversFailedOrRetiredProvider(t *testing.T) {
	for _, mode := range []string{"failure", "removed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p := NewSourcePool(ctx, "base")
			defer p.Close()
			cfg := []chains.Network{{Name: "base", URL: "http://first.test"}, {Name: "base", URL: "http://second.test"}}
			if err := p.SetNodes(cfg); err != nil {
				t.Fatal(err)
			}
			var tip atomic.Int64
			tip.Store(110)
			entered := make(chan struct{})
			release := make(chan struct{})
			var primary atomic.Int32
			p.open = func(_ context.Context, n chains.Network) (chains.Source, error) {
				return &movingSource{tip: &tip, read: func(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
					if n == cfg[0] && a == 100 {
						if primary.Add(1) == 1 {
							close(entered)
						}
						select {
						case <-ctx.Done():
							return 0, nil, ctx.Err()
						case <-release:
							return 0, nil, errors.New("provider failure")
						}
					}
					return b, nil, nil
				}}, nil
			}
			if _, _, _, err := p.LiveWindow(ctx, ctx, time.Second); err != nil {
				t.Fatal(err)
			}
			got := map[int64]int{}
			finish := func(r chains.Span, _ chains.BlockLink, _ []chains.Activity, err error) error {
				if err == nil {
					got[r.First]++
				}
				return err
			}
			if _, err := p.AdvanceLive(ctx, []chains.Span{{First: 100, Last: 100}, {First: 101, Last: 101}}, finish); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if mode == "failure" {
				close(release)
			} else if err := p.SetNodes(cfg[1:]); err != nil {
				t.Fatal(err)
			}
			for len(got) < 2 {
				if _, err := p.AdvanceLive(ctx, nil, finish); err != nil {
					t.Fatal(err)
				}
			}
			if got[100] != 1 || got[101] != 1 || primary.Load() != 1 {
				t.Fatal(got, primary.Load())
			}
		})
	}
}
