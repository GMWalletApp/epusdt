package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

// Reserve a single range as the runner would, then commit its deliveries.
func completeNext(s *Store, ctx context.Context, network string, prev, end int64, events []chains.Activity) error {
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	work, _, err := s.plan(ctx, network, prev, end, 16)
	if err != nil {
		return err
	}
	if len(work) != 1 {
		return fmt.Errorf("unexpected work %v", work)
	}
	return s.complete(ctx, network, work[0], chains.BlockLink{}, events)
}
func TestRegistrationBoundaries(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cursors.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w := feed.Watch{Network: "arbitrum", Address: wallet}
	rows, err := s.Subscribe(ctx, "merchant", []feed.Watch{w})
	if !errors.Is(err, feed.ErrCursorNotReady) {
		t.Fatal(rows, err)
	}
	if err = s.SeedCursor(ctx, w.Network, 99); err != nil {
		t.Fatal(err)
	}
	srvHandler, err := ConsumerHandler(s, map[string]string{"merchant": "012345678901234567890123"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(srvHandler)
	c := feed.Client{URL: srv.URL, Token: "012345678901234567890123"}
	rows, err = c.Subscribe(ctx, "", []feed.Watch{w, w})
	if err != nil || len(rows.Registrations) != 1 || rows.Registrations[0].FromHeight != 100 {
		t.Fatal(rows, err)
	}
	ready := make(chan *feed.Delivery, 1)
	go func() { d, _ := c.PendingNetwork(ctx, "", "arbitrum", 3*time.Second); ready <- d }()
	if err = completeNext(s, ctx, w.Network, 99, 100, []chains.Activity{{Height: 100, TxID: "payment", Addresses: []string{wallet}}}); err != nil {
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
	rows, err = s.Subscribe(ctx, "merchant", []feed.Watch{w})
	if err != nil || rows.Registrations[0].FromHeight != 100 {
		t.Fatal(rows, err)
	}
	_, err = s.Subscribe(ctx, "merchant", []feed.Watch{{Network: "sui", Address: "0x1"}})
	if !errors.Is(err, feed.ErrCursorNotReady) {
		t.Fatal(err)
	}
	if err = completeNext(s, ctx, w.Network, 100, 101, nil); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Subscribe(ctx, "merchant", []feed.Watch{w})
	if err != nil || rows.Registrations[0].FromHeight != 100 {
		t.Fatal("failed replacement removed watch", rows, err)
	}
	s.resolve = func(context.Context, string) (int64, error) { return 101, nil }
	if _, err = s.Subscribe(ctx, "merchant", nil); err != nil {
		t.Fatal(err)
	}
	s.resolve = func(context.Context, string) (int64, error) { return 299, nil }
	rows, err = s.Subscribe(ctx, "merchant", []feed.Watch{w})
	if err != nil || rows.Registrations[0].FromHeight != 300 {
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
			reg, err := s.Subscribe(ctx, c, []feed.Watch{{Network: "optimism", Address: wallet}})
			registered.Done()
			if err != nil {
				t.Error(err)
				return
			}
			d, err := s.PendingNetwork(ctx, c, "optimism", 3*time.Second)
			if err != nil || d == nil || d.Activity.Height != reg.Registrations[0].FromHeight {
				t.Error(c, reg, d, err)
			}
		}()
	}
	for h := int64(1); h <= 50; h++ {
		if err = completeNext(s, ctx, "optimism", h-1, h, []chains.Activity{{Height: h, TxID: fmt.Sprint(h), Addresses: []string{wallet}}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	registered.Wait()
	if err = completeNext(s, ctx, "optimism", 50, 51, []chains.Activity{{Height: 51, TxID: "51", Addresses: []string{wallet}}}); err != nil {
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
	watches := make([]feed.Watch, 10000)
	for i := range watches {
		watches[i] = feed.Watch{Network: "ethereum", Address: fmt.Sprintf("0x%040x", i+1)}
	}
	if err = s.SeedCursor(ctx, "ethereum", 0); err != nil {
		b.Fatal(err)
	}
	if _, err = s.Subscribe(ctx, "merchant", watches); err != nil {
		b.Fatal(err)
	}
	if err = s.SeedCursor(ctx, "ethereum", 0); err != nil {
		b.Fatal(err)
	}
	e := []chains.Activity{{Height: 1, TxID: "transfer", Addresses: []string{watches[9999].Address}}}
	if err = completeNext(s, ctx, "ethereum", 0, 1, e); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e[0].Height = int64(i + 2)
		if err = completeNext(s, ctx, "ethereum", int64(i+1), int64(i+2), e); err != nil {
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
	handler, err := ConsumerHandler(s, map[string]string{"a": token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := feed.Client{URL: srv.URL, Token: token}
	nets := []string{"optimism", "arbitrum"}
	var watches []feed.Watch
	for _, n := range nets {
		if err = s.SeedCursor(ctx, n, 1); err != nil {
			t.Fatal(err)
		}
		watches = append(watches, feed.Watch{Network: n, Address: wallet})
	}
	if _, err = client.Subscribe(ctx, "", watches); err != nil {
		t.Fatal(err)
	}
	for _, n := range nets {
		if err = completeNext(s, ctx, n, 1, 2, []chains.Activity{{Height: 2, TxID: n, Addresses: []string{wallet}}}); err != nil {
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
	d, err = client.PendingNetwork(ctx, "", "optimism", 0)
	if err != nil || d == nil || d.Activity.TxID != "optimism" {
		t.Fatal(d, err)
	}
}

func TestRetiredMorphWatchDoesNotBlockOtherSubscriptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scanner.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`INSERT INTO watches VALUES('pay','morph','old',10);
 INSERT INTO closing_watches VALUES('pay','morph','previous',3,8);
 INSERT INTO subscription_receipts VALUES('pay','[{"network":"morph","address":"previous","from_height":3,"to_height":8}]');
 INSERT INTO watches VALUES('pay','base',?,5)`, wallet)
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Watching("morph") || !store.Watching("base") {
		t.Fatal("unexpected restored watch state")
	}
	base := feed.Watch{Network: "base", Address: wallet}
	receipt, err := store.Subscribe(context.Background(), "pay", []feed.Watch{base})
	if err != nil || len(receipt.Terminations) != 0 || len(receipt.Registrations) != 1 || receipt.Registrations[0].FromHeight != 5 {
		t.Fatal(receipt, err)
	}
	if _, err = store.Subscribe(context.Background(), "pay", []feed.Watch{{Network: "morph", Address: wallet}}); err == nil {
		t.Fatal("accepted retired network")
	}
}

func TestMatchedScanSourcesSurviveAckAndRestart(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "scan.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w := feed.Watch{Network: "base", Address: wallet}
	if err := store.SeedCursor(ctx, w.Network, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Subscribe(ctx, "merchant", []feed.Watch{w}); err != nil {
		t.Fatal(err)
	}
	paired := chains.Network{Name: w.Network, URL: "wss://stream.example", HistoryURL: "https://history.example/api-key"}
	row := chains.Activity{Height: 10, TxID: "0x" + strings.Repeat("ab", 32), Addresses: []string{wallet}, ScanSources: chains.SourceIDs(paired)}
	if err := completeNext(store, ctx, w.Network, 9, 10, []chains.Activity{row}); err != nil {
		t.Fatal(err)
	}
	d, err := store.PendingNetwork(ctx, "merchant", w.Network, 0)
	if err != nil || d == nil {
		t.Fatal(d, err)
	}
	if err := store.Ack(ctx, "merchant", d.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.db.Query("SELECT source FROM transaction_sources WHERE network=? AND hash=?", w.Network, row.TxID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	scanned := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		scanned[key] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"https://stream.example:443", "https://HISTORY.example/other"} {
		if !chains.SharesScanSource(chains.Network{Name: "base", URL: url}, scanned) {
			t.Fatalf("lost scan source %s", url)
		}
	}
	if chains.SharesScanSource(chains.Network{Name: "base", URL: "https://other.example"}, scanned) {
		t.Fatal("independent endpoint excluded")
	}
}
