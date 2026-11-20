package scanner

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcreteEVMNetworkDemand(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var baseCalls, ethCalls, tip atomic.Int64
	tip.Store(50)
	node := func(id string, calls *atomic.Int64) func(rpcRequest) (any, error) {
		return func(r rpcRequest) (any, error) {
			calls.Add(1)
			switch r.Method {
			case "eth_chainId":
				return id, nil
			case "eth_blockNumber":
				return fmt.Sprintf("0x%x", tip.Load()), nil
			}
			return nil, fmt.Errorf("unexpected read %s", r.Method)
		}
	}
	eth := rpcFixture(t, node("0x1", &ethCalls))
	defer eth.Close()
	base := rpcFixture(t, node("0x2105", &baseCalls))
	defer base.Close()
	ns := []Network{{Name: "ethereum", URL: eth.URL}, {Name: "base", URL: base.URL}}
	registry, e := NewNodeRegistry(ctx, s, ns)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{}, 2)
	for _, n := range ns {
		go func() { registry.Run(ctx, n); done <- struct{}{} }()
	}
	defer func() { stop(); <-done; <-done }()
	for _, name := range []string{"", "evm", "optimism"} {
		if _, e = s.SubscribeFrom(ctx, "a", []Watch{{name, wallet}}); e == nil {
			t.Fatal("accepted missing/unconfigured chain", name)
		}
	}
	if baseCalls.Load()+ethCalls.Load() != 0 {
		t.Fatal("called idle node")
	}
	baseWatch := []Watch{{"base", wallet}}
	reg, e := s.SubscribeFrom(ctx, "a", baseWatch)
	if e != nil || len(reg) != 1 || reg[0].FromHeight != 51 {
		t.Fatal(reg, e)
	}
	if _, e = s.SubscribeFrom(ctx, "b", baseWatch); e != nil {
		t.Fatal(e)
	}
	if e = s.Subscribe(ctx, "a", nil); e != nil || !s.Watching("base") {
		t.Fatal("lost second consumer", e)
	}
	if ethCalls.Load() != 0 || s.Watching("ethereum") {
		t.Fatal("Base registration woke Ethereum")
	}
	if e = s.Subscribe(ctx, "b", nil); e != nil {
		t.Fatal(e)
	}
	if h, _ := s.Height(ctx, "base"); h != -1 || s.Watching("base") {
		t.Fatal("idle cursor", h)
	}
	tip.Store(500)
	reg, e = s.SubscribeFrom(ctx, "b", baseWatch)
	if e != nil || reg[0].FromHeight != 501 {
		t.Fatal(reg, e)
	}
	if _, e = s.SubscribeFrom(ctx, "a", []Watch{{"ethereum", wallet}}); e != nil {
		t.Fatal(e)
	}
	if e = s.Subscribe(ctx, "b", nil); e != nil || !s.Watching("ethereum") || s.Watching("base") {
		t.Fatal("mixed EVM chains", e)
	}
}

type lateSource struct{ entered, release chan struct{} }

func (*lateSource) Head(context.Context) (int64, error) { return 20, nil }
func (*lateSource) Close()                              {}
func (l *lateSource) Read(ctx context.Context, h, head int64) (int64, []Activity, error) {
	close(l.entered)
	<-l.release
	return h, []Activity{{Height: h, TxID: "stale", Addresses: []string{wallet}}}, nil
}
func TestIdleCancelsGenerationAndRejectsLateRead(t *testing.T) {
	ctx := context.Background()
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	w := []Watch{{"base", wallet}}
	if e = s.Subscribe(ctx, "a", w); e != nil {
		t.Fatal(e)
	}
	old, _ := s.period("base")
	src := &lateSource{make(chan struct{}), make(chan struct{})}
	done := make(chan error)
	go func() { _, e := s.Step(ctx, Network{Name: "base", Lookback: 1}, src); done <- e }()
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
	enqueue := func(s *Store) *Delivery {
		if e := s.Subscribe(ctx, "a", []Watch{{"ethereum", wallet}}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Step(ctx, Network{Name: "ethereum", Lookback: 1}, &chain{height: 99}); e != nil {
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
