package engine

import (
	"context"
	"errors"
	"testing"
	"time"

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
func TestIdleCancelsGenerationAndRejectsLateRead(t *testing.T) {
	ctx := context.Background()
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	w := []feed.Watch{{Network: "base", Address: wallet}}
	if e = s.Subscribe(ctx, "a", w); e != nil {
		t.Fatal(e)
	}
	old, _ := s.period("base")
	src := &lateSource{make(chan struct{}), make(chan struct{})}
	done := make(chan error)
	go func() { _, e := s.Step(ctx, chains.Network{Name: "base", Lookback: 1}, src); done <- e }()
	<-src.entered
	if e = s.Subscribe(ctx, "a", nil); e != nil {
		t.Fatal(e)
	}
	select {
	case <-old.Done():
	case <-time.After(time.Second):
		t.Fatal("active node context survived unsubscribe")
	}
	s.resolve = func(context.Context, string) (int64, error) { return 19, nil }
	if _, e = s.SubscribeFrom(ctx, "b", w); e != nil {
		t.Fatal(e)
	}
	close(src.release)
	if e = <-done; e != nil && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if d, e := s.Pending(ctx, "b"); d != nil || e != nil {
		t.Fatal("delivered stale generation", d, e)
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
		if e := s.Subscribe(ctx, "a", []feed.Watch{{Network: "ethereum", Address: wallet}}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Step(ctx, chains.Network{Name: "ethereum", Lookback: 1}, &chain{height: 99}); e != nil {
			t.Fatal(e)
		}
		d, e := s.Pending(ctx, "a")
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
	if d, e := next.Pending(ctx, "a"); e != nil || d != nil {
		t.Fatal(d, e)
	}
	current := enqueue(next)
	if e := next.Ack(ctx, "a", old.ID); e != nil {
		t.Fatal(e)
	}
	if d, e := next.Pending(ctx, "a"); e != nil || d == nil || d.ID != current.ID {
		t.Fatal("stale ACK removed a new delivery", d, e)
	}
}
