package nodes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
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
	go func() { defer close(exited); _, e := store.Step(ctx, cfg[0], p); done <- e }()
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
		from := int64(100 + 4*i)
		if q := initial[from]; q.node != proto || q.to != from+3 {
			t.Fatalf("unexpected range %+v", q)
		}
	}
	initial[108].reply <- nil
	ahead := get()
	if ahead.node != "jsonrpc" || ahead.from != 112 || ahead.to != 115 {
		t.Fatalf("fast node waited or changed range: %+v", ahead)
	}
	initial[104].reply <- errors.New("unavailable")
	select {
	case <-failed:
	case <-ctx.Done():
		t.Fatal("slow node blocked failover")
	}
	initial[100].reply <- nil
	retry := get()
	if retry.node != "http" || retry.from != 104 || retry.to != 107 {
		t.Fatalf("failed range: %+v", retry)
	}
	if h, e := store.Height(ctx, "tron"); e != nil || h != 103 {
		t.Fatalf("cursor before gap: %d %v", h, e)
	}
	ahead.reply <- nil
	retry.reply <- nil
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if h, e := store.Height(ctx, "tron"); e != nil || h != 115 {
		t.Fatalf("final cursor: %d %v", h, e)
	}
	for h := int64(100); h <= 115; h++ {
		d, e := store.Pending(ctx, "pay")
		if e != nil || d == nil || d.Activity.Height != h {
			t.Fatalf("delivery at %d: %+v %v", h, d, e)
		}
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
	if _, _, err := p.Read(ctx, 42, 42); err == nil || calls != 4 {
		t.Fatal("failed probe did not run")
	}
	now = now.Add(59 * time.Second)
	reject = false
	if _, _, err := p.Read(ctx, 42, 42); err == nil || calls != 4 {
		t.Fatal("probe was not isolated")
	}
	now = now.Add(time.Second)
	if end, _, err := p.Read(ctx, 42, 42); end != 42 || err != nil || calls != 5 {
		t.Fatal(end, err, calls)
	}
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
