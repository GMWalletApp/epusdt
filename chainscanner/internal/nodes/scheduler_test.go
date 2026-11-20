package nodes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

type mockLane struct {
	head int64
	read func(context.Context, int64, int64) (int64, []chains.Activity, error)
}

func (s *mockLane) Head(context.Context) (int64, error) { return s.head, nil }
func (s *mockLane) Read(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
	return s.read(ctx, a, b)
}
func (*mockLane) Close() {}

func TestMixedProtocolsDispatchAndCommitWithoutBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := []chains.Network{
		{Name: "tron", Protocol: "http", URL: "http://a.test"},
		{Name: "tron", Protocol: "grpc", URL: "grpc://b.test:50051"},
		{Name: "tron", Protocol: "jsonrpc", URL: "https://c.test/jsonrpc", HistoryURL: "https://c.test/history"},
	}
	p := NewSourcePool(ctx, "tron")
	if err := p.SetNodes(cfg); err != nil {
		t.Fatal(err)
	}
	type request struct {
		node     string
		from, to int64
		reply    chan error
	}
	started := make(chan request, 8)
	p.open = func(_ context.Context, c chains.Network) (chains.Source, error) {
		return &mockLane{115, func(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
			q := request{c.Protocol, a, b, make(chan error, 1)}
			started <- q
			select {
			case <-ctx.Done():
				return 0, nil, ctx.Err()
			case err := <-q.reply:
				if err != nil {
					return 0, nil, err
				}
			}
			var rows []chains.Activity
			for h := a; h <= b; h++ {
				rows = append(rows, chains.Activity{Height: h, TxID: fmt.Sprint(h), Addresses: []string{"T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb"}})
			}
			return b, rows, nil
		}}, nil
	}
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Subscribe(ctx, "pay", []feed.Watch{{Network: "tron", Address: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb"}}); err != nil {
		t.Fatal(err)
	}
	if err = store.SeedCursor(ctx, "tron", 99); err != nil {
		t.Fatal(err)
	}
	failed := make(chan struct{}, 1)
	p.Report = func(n chains.Network, ok bool) {
		if !ok {
			failed <- struct{}{}
		}
	}
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		for {
			behind, e := store.Step(ctx, cfg[0], p)
			if e != nil || !behind {
				done <- e
				return
			}
		}
	}()
	defer func() { cancel(); <-exited; p.Close() }()
	get := func() request {
		t.Helper()
		select {
		case q := <-started:
			return q
		case <-ctx.Done():
			t.Fatal("dispatch stalled")
			return request{}
		}
	}
	initial := map[int64]request{}
	for range 3 {
		q := get()
		initial[q.from] = q
	}
	for i, proto := range []string{"http", "grpc", "jsonrpc"} {
		from := int64(100 + i)
		if q := initial[from]; q.node != proto || q.to != from {
			t.Fatalf("unexpected range %+v", q)
		}
	}
	initial[102].reply <- nil
	ahead := get()
	if ahead.node != "jsonrpc" || ahead.from != 103 {
		t.Fatalf("fast node waited: %+v", ahead)
	}
	first, e := store.Pending(ctx, "pay")
	if e != nil || first == nil || first.Activity.Height != 102 {
		t.Fatalf("102 not delivered across two holes: %+v %v", first, e)
	}
	if h, e := store.Height(ctx, "tron"); e != nil || h != 99 {
		t.Fatal(h, e)
	}
	initial[101].reply <- errors.New("temporary read failure")
	select {
	case <-failed:
	case <-ctx.Done():
		t.Fatal("no failure report")
	}
	initial[100].reply <- nil
	ahead.reply <- nil
	// One outstanding request per lane. The failed block retries on another provider.
	for remaining := 13; remaining > 0; remaining-- {
		q := get()
		if q.from == 101 && q.node == "grpc" {
			t.Fatal("no failover")
		}
		q.reply <- nil
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if h, e := store.Height(ctx, "tron"); e != nil || h != 115 {
		t.Fatal(h, e)
	}
	seen := map[int64]bool{}
	for range 16 {
		d, e := store.Pending(ctx, "pay")
		if e != nil || d == nil || d.Activity.Height < 100 || d.Activity.Height > 115 || seen[d.Activity.Height] {
			t.Fatalf("unexpected delivery %+v %v", d, e)
		}
		seen[d.Activity.Height] = true
		if e = store.Ack(ctx, "pay", d.ID); e != nil {
			t.Fatal(e)
		}
	}
	if d, e := store.Pending(ctx, "pay"); e != nil || d != nil {
		t.Fatal("unexpected extra delivery", d, e)
	}

}

func TestReadCooldownSurvivesHeadRefreshAndReopens(t *testing.T) {
	ctx := context.Background()
	p := NewSourcePool(ctx, "base")
	cfg := []chains.Network{{Name: "base", URL: "http://a.test"}}
	now := time.Now()
	p.now = func() time.Time { return now }
	calls := 0
	reject := true
	p.open = func(context.Context, chains.Network) (chains.Source, error) {
		return &mockLane{100, func(context.Context, int64, int64) (int64, []chains.Activity, error) {
			calls++
			if reject {
				return 0, nil, errors.New("429")
			}
			return 42, nil, nil
		}}, nil
	}
	defer p.Close()
	for range 3 {
		if err := p.SetNodes(cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Head(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := p.Read(ctx, 42, 42); err == nil {
			t.Fatal("failed block reported complete")
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	for range 3 {
		if _, err := p.Head(ctx); err == nil {
			t.Fatal("queried cooling node")
		}
		if _, _, err := p.Read(ctx, 42, 42); err == nil {
			t.Fatal("read from cooling node")
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	now = now.Add(time.Minute)
	for _, cooldown := range []time.Duration{3 * time.Minute, 6 * time.Minute, 10 * time.Minute} {
		for range 3 {
			if _, err := p.Head(ctx); err != nil {
				t.Fatal("height checks must not erase block-read penalties", err)
			}
			before := calls
			if _, _, err := p.Read(ctx, 42, 42); err == nil || calls != before+1 {
				t.Fatal("expected an actual failed block read", calls)
			}
		}
		if wait := p.nodes[0].retry.Sub(now); wait != cooldown {
			t.Fatal("wrong cooldown", wait, cooldown)
		}
		now = now.Add(cooldown - time.Second)
		if _, err := p.Head(ctx); err == nil {
			t.Fatal("tried cooling endpoint")
		}
		now = now.Add(time.Second)
	}
	reject = false
	if end, _, err := p.Read(ctx, 42, 42); end != 42 || err != nil {
		t.Fatal(end, err)
	}
	reject = true
	for range 3 {
		_, _, _ = p.Read(ctx, 42, 42)
	}
	if wait := p.nodes[0].retry.Sub(now); wait != time.Minute {
		t.Fatal("successful read did not reset penalty", wait)
	}
	now = now.Add(time.Minute)
	if _, err := p.Head(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.SetNodes([]chains.Network{{Name: "ethereum", URL: "http://b.test"}}); err == nil {
		t.Fatal("accepted wrong network")
	}
}

func TestPartialLedgerPagesAndAllNodesUnavailable(t *testing.T) {
	ctx := context.Background()
	p := NewSourcePool(ctx, "aptos")
	if e := p.SetNodes([]chains.Network{{Name: "aptos", URL: "https://a.test"}}); e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	p.open = func(context.Context, chains.Network) (chains.Source, error) {
		return &mockLane{100, func(_ context.Context, a, b int64) (int64, []chains.Activity, error) {
			if a == 5 {
				return 0, nil, errors.New("no history")
			}
			return a, []chains.Activity{{Height: a, TxID: fmt.Sprint(a)}}, nil
		}}, nil
	}
	var heights []int64
	if e := p.Scan(ctx, 1, 3, func(_ int64, rows []chains.Activity) error {
		for _, r := range rows {
			heights = append(heights, r.Height)
		}
		return nil
	}); e != nil || !slices.Equal(heights, []int64{1, 2, 3}) {
		t.Fatal(heights, e)
	}
	if end, rows, e := p.Read(ctx, 4, 6); e == nil || len(rows) != 0 || end != 0 {
		t.Fatal(end, rows, e)
	}
}

func TestSlowGapUsesFullLookahead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p := NewSourcePool(ctx, "tron")
		var configs []chains.Network
		for i := range 8 {
			configs = append(configs, chains.Network{Name: "tron", URL: fmt.Sprintf("http://node%d.test", i)})
		}
		if err := p.SetNodes(configs); err != nil {
			t.Fatal(err)
		}
		const start = int64(100)
		const head = start + 4*48 - 1
		release := make(chan struct{})
		var reads sync.Map
		p.open = func(context.Context, chains.Network) (chains.Source, error) {
			return &mockLane{head, func(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
				if _, dup := reads.LoadOrStore(a, true); dup {
					return 0, nil, fmt.Errorf("duplicate range %d", a)
				}
				if a == 104 {
					select {
					case <-release:
					case <-ctx.Done():
						return 0, nil, ctx.Err()
					}
				}
				return b, []chains.Activity{{Height: b}}, nil
			}}, nil
		}
		var ends []int64
		var scanErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			scanErr = p.Scan(ctx, start, head, func(h int64, rows []chains.Activity) error {
				if len(rows) != 1 || rows[0].Height != h {
					return fmt.Errorf("wrong rows for %d", h)
				}
				ends = append(ends, h)
				return nil
			})
		}()
		defer func() { cancel(); <-done; p.Close() }()
		synctest.Wait() // all fast lanes have exhausted the window while 104 is still blocked
		if !slices.Equal(ends, []int64{103}) {
			t.Fatalf("committed past gap: %v", ends)
		}
		for i := 0; i <= 32; i++ {
			if _, ok := reads.Load(start + int64(4*i)); !ok {
				t.Fatalf("fast nodes stopped before range %d", i)
			}
		}
		if _, ok := reads.Load(start + 4*33); ok {
			t.Fatal("exceeded 32-range sliding window")
		}
		close(release)
		<-done
		if scanErr != nil || len(ends) != 48 {
			t.Fatalf("completed %d ranges: %v", len(ends), scanErr)
		}
		for i, h := range ends {
			if h != start+int64(4*i)+3 {
				t.Fatal("out of order", i, h)
			}
		}
	})
}

func TestBytePressureAllowsOldestGapRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p := NewSourcePool(ctx, "tron")
		configs := []chains.Network{{Name: "tron", URL: "http://a.test"}, {Name: "tron", URL: "http://b.test"}}
		if err := p.SetNodes(configs); err != nil {
			t.Fatal(err)
		}
		reject := make(chan struct{})
		var calls atomic.Int32
		p.open = func(_ context.Context, cfg chains.Network) (chains.Source, error) {
			return &mockLane{115, func(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
				calls.Add(1)
				if a == 100 && cfg == configs[0] {
					select {
					case <-reject:
						return 0, nil, errors.New("unavailable")
					case <-ctx.Done():
						return 0, nil, ctx.Err()
					}
				}
				row := chains.Activity{Height: b}
				if a == 104 {
					row.Data = make([]byte, reorderBytes)
				}
				return b, []chains.Activity{row}, nil
			}}, nil
		}
		var ends []int64
		var scanErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			scanErr = p.Scan(ctx, 100, 115, func(h int64, _ []chains.Activity) error { ends = append(ends, h); return nil })
		}()
		defer func() { cancel(); <-done; p.Close() }()
		synctest.Wait()
		if calls.Load() != 2 || len(ends) != 0 {
			t.Fatalf("read through byte pressure: %d calls, commits %v", calls.Load(), ends)
		}
		close(reject)
		<-done
		if scanErr != nil || calls.Load() != 5 || !slices.Equal(ends, []int64{103, 107, 111, 115}) {
			t.Fatal(scanErr, calls.Load(), ends)
		}
	})
}

func TestEightLanesRepairSlowMiddleRange(t *testing.T) {
	for _, mode := range []string{"ordered", "delivery"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				p := NewSourcePool(ctx, "tron")
				var cfg []chains.Network
				for i := range 9 {
					cfg = append(cfg, chains.Network{Name: "tron", URL: fmt.Sprintf("http://node%d.test", i)})
				}
				if err := p.SetNodes(cfg); err != nil {
					t.Fatal(err)
				}
				type read struct {
					from, to int64
					url      string
				}
				started := make(chan read, 24)
				release := make(chan struct{})
				p.open = func(_ context.Context, n chains.Network) (chains.Source, error) {
					return &mockLane{111, func(ctx context.Context, a, b int64) (int64, []chains.Activity, error) {
						started <- read{a, b, n.URL}
						if a == 104 && n.URL == cfg[4].URL {
							<-ctx.Done()
							return 0, nil, ctx.Err()
						}
						select {
						case <-release:
							return b, nil, nil
						case <-ctx.Done():
							return 0, nil, ctx.Err()
						}
					}}, nil
				}
				var ends []int64
				var reported atomic.Int32
				var repaired atomic.Bool
				var scanErr error
				done := make(chan struct{})
				go func() {
					defer close(done)
					emit := func(h int64, _ []chains.Activity) error {
						ends = append(ends, h)
						reported.Add(1)
						if h == 104 {
							repaired.Store(true)
						}
						return nil
					}
					if mode == "ordered" {
						scanErr = p.Scan(ctx, 100, 111, emit)
						return
					}
					var spans []chains.Span
					for h := int64(100); h <= 111; h++ {
						spans = append(spans, chains.Span{First: h, Last: h})
					}
					scanErr = p.ScanPending(ctx, spans, func(r chains.Span, _ chains.BlockLink, rows []chains.Activity, err error) error {
						if err != nil {
							return err
						}
						return emit(r.Last, rows)
					})
				}()
				defer func() { cancel(); <-done; p.Close() }()
				synctest.Wait()
				if len(started) != 8 {
					t.Fatalf("simultaneous starts: %d", len(started))
				}
				for range 8 {
					r := <-started
					if r.to != r.from || r.from < 100 || r.from > 107 || r.url != cfg[r.from-100].URL {
						t.Fatal(r)
					}
				}
				close(release)
				synctest.Wait()
				want := int32(4)
				if mode == "delivery" {
					want = 11
				}
				if reported.Load() != want || repaired.Load() {
					t.Fatal("unexpected delivery before repair", mode, reported.Load())
				}
				time.Sleep(slowGapDelay - time.Millisecond)
				synctest.Wait()
				if repaired.Load() {
					t.Fatal("reassigned early")
				}
				time.Sleep(time.Millisecond)
				<-done
				if scanErr != nil || len(ends) != 12 {
					t.Fatal(ends, scanErr)
				}
				if mode == "delivery" {
					if ends[len(ends)-1] != 104 {
						t.Fatal("late gap not delivered", ends)
					}
					slices.Sort(ends)
				}
				for i, h := range ends {
					if h != 100+int64(i) {
						t.Fatal("out of order", ends)
					}
				}
				close(started)
				retries := 0
				for r := range started {
					if r.from == 104 {
						retries++
						if r.url == cfg[4].URL {
							t.Fatal("retried slow backend")
						}
					}
				}
				if retries != 1 || p.nodes[4].readFailures != (failureStreak{}) {
					t.Fatal("unexpected retry/penalty", retries)
				}
			})
		})
	}
}
