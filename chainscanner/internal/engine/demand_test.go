package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

type lateSource struct{ entered, release chan struct{} }

func (*lateSource) Head(context.Context) (int64, error) { return 20, nil }
func (*lateSource) Close()                              {}
func (l *lateSource) Read(ctx context.Context, h, head int64) (int64, []chains.Activity, error) {
	close(l.entered)
	<-l.release
	return h, []chains.Activity{{Height: h, TxID: "stale", Addresses: []string{wallet}}}, nil
}
func TestLastWatchFinishesInFlightRead(t *testing.T) {
	ctx := context.Background()
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.resolve = func(context.Context, string) (int64, error) { return 20, nil }
	if e = s.SeedCursor(ctx, "base", 19); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Subscribe(ctx, "a", []feed.Watch{{Network: "base", Address: wallet}}); e != nil {
		t.Fatal(e)
	}
	src := &lateSource{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, e := s.Step(ctx, chains.Network{Name: "base"}, src); done <- e }()
	<-src.entered
	receipt, e := s.Subscribe(ctx, "a", nil)
	if e != nil || len(receipt.Terminations) != 1 || receipt.Terminations[0].ToHeight != 20 || !s.Draining("base") {
		t.Fatal(receipt, e)
	}
	close(src.release)
	if e = <-done; e != nil && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if s.Watching("base") {
		t.Fatal("still running after inclusive terminal block")
	}
	d, e := s.PendingNetwork(ctx, "a", "base", 0)
	if d == nil || e != nil || d.Activity.Height != 20 {
		t.Fatal(d, e)
	}
}
func TestStatelessRestartRequiresRegistration(t *testing.T) {
	ctx := context.Background()
	open := func() *Store {
		s, e := Open(":memory:")
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	enqueue := func(s *Store) *feed.Delivery {
		if e := s.SeedCursor(ctx, "ethereum", 98); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Subscribe(ctx, "a", []feed.Watch{{Network: "ethereum", Address: wallet}}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Step(ctx, chains.Network{Name: "ethereum", Lookback: 1}, &chain{height: 99}); e != nil {
			t.Fatal(e)
		}
		d, e := s.PendingNetwork(ctx, "a", "ethereum", 0)
		if e != nil || d == nil {
			t.Fatal(d, e)
		}
		return d
	}
	first := open()
	old := enqueue(first)
	first.Close()
	next := open()
	defer next.Close()
	if next.Watching("ethereum") {
		t.Fatal("restored an old subscription")
	}
	if d, e := next.PendingNetwork(ctx, "a", "ethereum", 0); e != nil || d != nil {
		t.Fatal(d, e)
	}
	current := enqueue(next)
	if e := next.Ack(ctx, "a", old.ID); e != nil {
		t.Fatal(e)
	}
	if d, e := next.PendingNetwork(ctx, "a", "ethereum", 0); e != nil || d == nil || d.ID != current.ID {
		t.Fatal("stale ACK removed a new delivery", d, e)
	}
}
