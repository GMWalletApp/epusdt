package scanner

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRegistrationBoundaries(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cursors.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w := Watch{"arbitrum", wallet}
	rows, err := s.SubscribeFrom(ctx, "merchant", []Watch{w})
	if !errors.Is(err, ErrCursorNotReady) {
		t.Fatal(rows, err)
	}
	if err = s.SeedCursor(ctx, w.Network, 99); err != nil {
		t.Fatal(err)
	}
	srvHandler, err := Handler(s, map[string]string{"merchant": "012345678901234567890123"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(srvHandler)
	c := Client{srv.URL, "012345678901234567890123"}
	rows, err = c.SubscribeFrom(ctx, "", []Watch{w, w})
	if err != nil || len(rows) != 1 || rows[0].FromHeight != 100 {
		t.Fatal(rows, err)
	}
	ready := make(chan *Delivery, 1)
	go func() { d, _ := c.PendingWait(ctx, "", 3*time.Second); ready <- d }()
	if err = s.commit(ctx, w.Network, 99, 100, []Activity{{Height: 100, TxID: "payment", Addresses: []string{wallet}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-ready:
		if d == nil || d.Activity.Height != 100 {
			t.Fatal(d)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("long poll missed commit")
	}
	srv.Close()
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err = s.SubscribeFrom(ctx, "merchant", []Watch{w})
	if err != nil || rows[0].FromHeight != 100 {
		t.Fatal(rows, err)
	}
	_, err = s.SubscribeFrom(ctx, "merchant", []Watch{{"sui", "0x1"}})
	if !errors.Is(err, ErrCursorNotReady) {
		t.Fatal(err)
	}
	if err = s.commit(ctx, w.Network, 100, 101, nil); err != nil {
		t.Fatal(err)
	}
	rows, err = s.SubscribeFrom(ctx, "merchant", []Watch{w})
	if err != nil || rows[0].FromHeight != 100 {
		t.Fatal("failed replacement removed watch", rows, err)
	}
	if err = s.Subscribe(ctx, "merchant", nil); err != nil {
		t.Fatal(err)
	}
	s.resolve = func(context.Context, string) (int64, error) { return 299, nil }
	rows, err = s.SubscribeFrom(ctx, "merchant", []Watch{w})
	if err != nil || rows[0].FromHeight != 300 {
		t.Fatal(rows, err)
	}
}
func TestConcurrentRegisterAndCommit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SeedCursor(ctx, "optimism", 0); err != nil {
		t.Fatal(err)
	}
	var wg, registered sync.WaitGroup
	registered.Add(64)
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := fmt.Sprint(i)
			reg, err := s.SubscribeFrom(ctx, c, []Watch{{"optimism", wallet}})
			registered.Done()
			if err != nil {
				t.Error(err)
				return
			}
			d, err := s.PendingWait(ctx, c, 3*time.Second)
			if err != nil || d == nil || d.Activity.Height != reg[0].FromHeight {
				t.Error(c, reg, d, err)
			}
		}()
	}
	for h := int64(1); h <= 50; h++ {
		if err = s.commit(ctx, "optimism", h-1, h, []Activity{{Height: h, TxID: fmt.Sprint(h), Addresses: []string{wallet}}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	registered.Wait()
	if err = s.commit(ctx, "optimism", 50, 51, []Activity{{Height: 51, TxID: "51", Addresses: []string{wallet}}}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}
func BenchmarkCommitIndexedWatches(b *testing.B) {
	s, err := Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	watches := make([]Watch, 10000)
	for i := range watches {
		watches[i] = Watch{"ethereum", fmt.Sprintf("0x%040x", i+1)}
	}
	if err = s.Subscribe(ctx, "merchant", watches); err != nil {
		b.Fatal(err)
	}
	if err = s.SeedCursor(ctx, "ethereum", 0); err != nil {
		b.Fatal(err)
	}
	e := []Activity{{TxID: "transfer", Addresses: []string{watches[9999].Address}}}
	if err = s.commit(ctx, "ethereum", 0, 1, e); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err = s.commit(ctx, "ethereum", int64(i+1), int64(i+2), e); err != nil {
			b.Fatal(err)
		}
	}
}

func TestUnacknowledgedNetworkDoesNotBlockOthers(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token := "012345678901234567890123"
	handler, err := Handler(s, map[string]string{"a": token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := Client{srv.URL, token}
	nets := []string{"optimism", "arbitrum"}
	var watches []Watch
	for _, n := range nets {
		if err = s.SeedCursor(ctx, n, 1); err != nil {
			t.Fatal(err)
		}
		watches = append(watches, Watch{n, wallet})
	}
	if _, err = client.SubscribeFrom(ctx, "", watches); err != nil {
		t.Fatal(err)
	}
	for _, n := range nets {
		if err = s.commit(ctx, n, 1, 2, []Activity{{Height: 2, TxID: n, Addresses: []string{wallet}}}); err != nil {
			t.Fatal(err)
		}
	}
	d, err := client.PendingNetwork(ctx, "", "arbitrum", time.Second)
	if err != nil || d == nil || d.Activity.TxID != "arbitrum" {
		t.Fatal(d, err)
	}
	if err = client.Ack(ctx, "", d.ID); err != nil {
		t.Fatal(err)
	}
	d, err = client.Pending(ctx, "")
	if err != nil || d == nil || d.Activity.TxID != "optimism" {
		t.Fatal(d, err)
	}
}
