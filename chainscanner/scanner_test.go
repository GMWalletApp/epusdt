package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const wallet = "0x1111111111111111111111111111111111111111"

type chain struct {
	height int64
	heads  int
	reads  int
	fail   bool
}

func (c *chain) Head(context.Context) (int64, error) { c.heads++; return c.height, nil }
func (c *chain) Close()                              {}
func (c *chain) Read(_ context.Context, h, head int64) (int64, []Activity, error) {
	c.reads++
	if c.fail {
		return 0, nil, errors.New("provider unavailable")
	}
	return h, []Activity{{Height: h, TxID: fmt.Sprint(h), Addresses: []string{wallet, wallet}, Data: json.RawMessage("{}")}}, nil
}
func TestSharedReadDurableFanout(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "service.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	w := []Watch{{"ethereum", wallet}}
	for _, c := range []string{"pay", "other"} {
		if e = s.Subscribe(ctx, c, w); e != nil {
			t.Fatal(e)
		}
	}
	src := &chain{height: 10}
	n := Network{Name: "ethereum", Confirmations: 2, Lookback: 1}
	if _, e = s.Step(ctx, n, src); e != nil {
		t.Fatal(e)
	}
	if src.reads != 1 {
		t.Fatal(src.reads)
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d, e := s.Pending(ctx, "pay")
	if e != nil || d == nil || d.Activity.Height != 9 || len(d.Activity.Addresses) != 1 {
		t.Fatal(d, e)
	}
	if e = s.Ack(ctx, "other", d.ID); e != nil {
		t.Fatal(e)
	}
	again, _ := s.Pending(ctx, "pay")
	if again == nil || again.ID != d.ID {
		t.Fatal("cross-consumer ack")
	}
	if e = s.Ack(ctx, "pay", d.ID); e != nil {
		t.Fatal(e)
	}
	if d, _ = s.Pending(ctx, "other"); d == nil {
		t.Fatal("lost offline delivery")
	}
	if _, e = s.Step(ctx, n, src); e != nil || src.reads != 1 {
		t.Fatal(e, src.reads)
	}
	src.height++
	src.fail = true
	if _, e = s.Step(ctx, n, src); e == nil {
		t.Fatal("failed read accepted")
	}
	src.fail = false
	if _, e = s.Step(ctx, n, src); e != nil {
		t.Fatal(e)
	}
	d, _ = s.Pending(ctx, "pay")
	if d == nil || d.Activity.Height != 10 {
		t.Fatal(d)
	}
}
func TestEmptySubscriptionMakesNoNodeCalls(t *testing.T) {
	networks := []string{"tron", "solana", "ton", "aptos", "sui"}
	for name := range evmIDs {
		networks = append(networks, name)
	}
	for _, net := range networks {
		t.Run(net, func(t *testing.T) {
			s, e := Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			src := &chain{height: 100}
			n := Network{Name: net}
			ctx := context.Background()
			for i := 0; i < 3; i++ {
				src.height++
				if _, e = s.Step(ctx, n, src); e != nil {
					t.Fatal(e)
				}
			}
			h, ok, e := s.cursor(ctx, net)
			if e != nil || ok || src.heads != 0 || src.reads != 0 {
				t.Fatal(h, src.reads, e)
			}
		})
	}
}
func TestAuthenticatedSubscription(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	token := strings.Repeat("a", 32)
	h, e := Handler(s, map[string]string{"pay": token})
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx := context.Background()
	c := Client{srv.URL, token}
	if e = (Client{srv.URL, "bad"}).Subscribe(ctx, "pay", nil); e == nil {
		t.Fatal("invalid token accepted")
	}
	if e = c.Subscribe(ctx, "ignored", []Watch{{"ethereum", wallet}}); e != nil {
		t.Fatal(e)
	}
	if e = c.Subscribe(ctx, "ignored", []Watch{{"ethereum", "bad"}}); e == nil {
		t.Fatal("invalid watch accepted")
	}
	if _, e = s.Step(ctx, Network{Name: "ethereum", Lookback: 1}, &chain{height: 25}); e != nil {
		t.Fatal(e)
	}
	d, e := c.Pending(ctx, "ignored")
	if e != nil || d == nil {
		t.Fatal(d, e)
	}
	if e = c.Ack(ctx, "ignored", d.ID); e != nil {
		t.Fatal(e)
	}
	d, e = c.Pending(ctx, "ignored")
	if d != nil || e != nil {
		t.Fatal(d, e)
	}
}
func TestAptosGapStopsCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/transactions" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `[{"version":"55","hash":"tx","sender":"0x1"},{"version":"57","hash":"tx2"}]`)
	}))
	defer srv.Close()
	src, e := NewSource(context.Background(), Network{Name: "aptos", URL: srv.URL + "/v1"})
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	if _, _, e = src.Read(context.Background(), 55, 57); e == nil {
		t.Fatal("accepted noncontiguous ledger")
	}
}

func TestDatabaseRejectsSecondScanner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	one, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	other, err := Open(path)
	if err == nil {
		other.Close()
		t.Fatal("two live scanners acquired the same database")
	}
}

func TestConcurrentStepsReadBlockOnce(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Subscribe(context.Background(), "test", []Watch{{"ethereum", wallet}}); err != nil {
		t.Fatal(err)
	}
	src := &chain{height: 100}
	done := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := s.Step(context.Background(), Network{Name: "ethereum", Lookback: 1}, src)
			done <- err
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if src.reads != 1 {
		t.Fatalf("read the same height %d times", src.reads)
	}
}
