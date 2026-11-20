package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

type gapSource struct {
	chain
	calls   []int64
	missing int64
}

func (s *gapSource) Read(ctx context.Context, first, last int64) (int64, []chains.Activity, error) {
	s.calls = append(s.calls, first)
	if first == s.missing {
		return 0, nil, errors.New("missing block")
	}
	return s.chain.Read(ctx, first, last)
}

func TestRestartOnlyReadsMissingAndNewBlocks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scanner.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	w := []feed.Watch{{Network: "base", Address: wallet}}
	cfg := chains.Network{Name: "base"}
	if err = s.SeedCursor(ctx, "base", 100); err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []string{"pay", "notification"} {
		if _, err = s.Subscribe(ctx, consumer, w); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SeedCursor(ctx, "base", 100); err != nil {
		t.Fatal(err)
	}
	src := &gapSource{chain: chain{height: 103}, missing: 101}
	if _, err = s.Step(ctx, cfg, src); err == nil {
		t.Fatal("gap disappeared")
	}
	if h, _ := s.Height(ctx, "base"); h != 100 {
		t.Fatal(h)
	}
	d, err := s.PendingNetwork(ctx, "pay", "base", 0)
	if err != nil || d == nil || d.Activity.Height != 102 {
		t.Fatal(d, err)
	}
	oldID := d.ID
	reg, err := s.Subscribe(ctx, "late", w)
	if err != nil || len(reg.Registrations) != 1 || reg.Registrations[0].FromHeight != 104 {
		t.Fatal(reg, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	src.missing = 0
	src.calls = nil
	src.height = 104
	if _, err = s.Step(ctx, cfg, src); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(src.calls, []int64{101, 104}) {
		t.Fatal("unexpected reads after restart", src.calls)
	}
	if h, _ := s.Height(ctx, "base"); h != 104 {
		t.Fatal(h)
	}
	for _, consumer := range []string{"pay", "notification", "late"} {
		var heights []int64
		for {
			d, err = s.PendingNetwork(ctx, consumer, "base", 0)
			if err != nil {
				t.Fatal(err)
			}
			if d == nil {
				break
			}
			if consumer == "pay" && len(heights) == 0 && d.ID != oldID {
				t.Fatal("delivery ID changed")
			}
			heights = append(heights, d.Activity.Height)
			if err = s.Ack(ctx, consumer, d.ID); err != nil {
				t.Fatal(err)
			}
		}
		want := []int64{102, 103, 101, 104}
		if consumer == "late" {
			want = []int64{104}
		}
		if !slices.Equal(heights, want) {
			t.Fatal(consumer, heights)
		}
	}
}

func TestGapOutboxRollbackAndNeighborConflict(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SeedCursor(ctx, "base", 100); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Subscribe(ctx, "pay", []feed.Watch{{Network: "base", Address: wallet}}); err != nil {
		t.Fatal(err)
	}
	jobs, _, err := s.plan(ctx, "base", 100, 103, 16)
	if err != nil {
		t.Fatal(err)
	}
	finish := func(i int, parent string) error {
		h := jobs[i].First
		return s.complete(ctx, "base", jobs[i], chains.BlockLink{Hash: fmt.Sprint(h), Parent: parent}, []chains.Activity{{Height: h, Addresses: []string{wallet}}})
	}
	if err = finish(2, "102"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("CREATE TRIGGER reject_delivery BEFORE INSERT ON deliveries BEGIN SELECT RAISE(ABORT,'test failure'); END"); err != nil {
		t.Fatal(err)
	}
	if finish(0, "100") == nil {
		t.Fatal("failed outbox insert accepted")
	}
	if _, err = s.db.Exec("DROP TRIGGER reject_delivery"); err != nil {
		t.Fatal(err)
	}
	if err = finish(0, "100"); err != nil {
		t.Fatal("rollback lost hole", err)
	}
	if err = finish(1, "conflicting"); !errors.Is(err, chains.ErrFork) {
		t.Fatal(err)
	}
	if h, _ := s.Height(ctx, "base"); h != 101 {
		t.Fatal(h)
	}
	if err = finish(1, "101"); err != nil {
		t.Fatal(err)
	}
	if err = finish(1, "101"); err == nil {
		t.Fatal("accepted duplicate completion")
	}
	var edges, deliveries int
	if err = s.db.QueryRow("SELECT (SELECT COUNT(*) FROM scan_edges),(SELECT COUNT(*) FROM deliveries)").Scan(&edges, &deliveries); err != nil || edges != 1 || deliveries != 3 {
		t.Fatal(edges, deliveries, err)
	}
	s.resolve = func(context.Context, string) (int64, error) { return 103, nil }
	if _, err = s.Subscribe(ctx, "pay", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("SELECT COUNT(*) FROM scan_edges").Scan(&edges); err != nil || edges != 0 {
		t.Fatal(edges, err)
	}
}

func TestAptosPartialPageAndGapSplit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SeedCursor(ctx, "aptos", 100); err != nil {
		t.Fatal(err)
	}
	jobs, _, err := s.plan(ctx, "aptos", 100, 200, 16)
	if err != nil || len(jobs) != 1 || jobs[0].Last != 200 {
		t.Fatal(jobs, err)
	}
	if err = s.complete(ctx, "aptos", chains.Span{First: 101, Last: 120}, chains.BlockLink{}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.deferGap(ctx, "aptos", chains.Span{First: 121, Last: 200}); err != nil {
		t.Fatal(err)
	}
	jobs, _, err = s.plan(ctx, "aptos", 200, 201, 16)
	want := []chains.Span{{First: 121, Last: 160, Failed: true}, {First: 201, Last: 201}, {First: 161, Last: 200, Failed: true}}
	if err != nil || !slices.Equal(jobs, want) {
		t.Fatal(jobs, err)
	}
}
