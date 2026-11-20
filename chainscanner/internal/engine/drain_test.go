package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

func TestUnsubscribeRepairsThroughInclusiveHeadAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "drain.sqlite")
	open := func() *Store {
		t.Helper()
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	defer func() { s.Close() }()
	const token = "000000000000000000000000"
	const watch = `[{"network":"base","address":"` + wallet + `"}]`
	request := func(method, path, body string, want int) feed.SubscriptionReceipt {
		t.Helper()
		h, err := ConsumerHandler(s, map[string]string{"pay": token})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: got %d %s, want %d", path, w.Code, w.Body, want)
		}
		var result feed.SubscriptionReceipt
		if want == 200 || want == 202 {
			if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	const route = "/v1/subscriptions"
	cfg := chains.Network{Name: "base", Confirmations: 3}
	src := &gapSource{chain: chain{height: 100}, missing: 101}
	ConfigureNetworks(s, map[string]chains.Network{"base": cfg}, func(ctx context.Context, _ string) (int64, error) { return src.Head(ctx) })
	if r := request("PUT", route, watch, 200); len(r.Registrations) != 1 || r.Registrations[0].FromHeight != 101 {
		t.Fatal(r)
	}
	src.height = 105
	if _, err := s.Step(ctx, cfg, src); err == nil {
		t.Fatal("expected missing 101")
	}
	s.resolve = nil
	request("PUT", route, "[]", 409)
	if r := request("PUT", route, watch, 200); len(r.Registrations) != 1 {
		t.Fatal(r)
	}
	s.resolve = func(context.Context, string) (int64, error) { return 105, nil }
	checkEnd := func(code int, completed bool) {
		t.Helper()
		r := request("PUT", route, "[]", code)
		if len(r.Registrations) != 0 || len(r.Terminations) != 1 {
			t.Fatal(r)
		}
		e := r.Terminations[0]
		if e.FromHeight != 101 || e.ToHeight != 105 || e.Completed != completed {
			t.Fatal(e)
		}
	}
	checkEnd(202, false)
	if !s.Draining("base") {
		t.Fatal("expected drain")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	checkEnd(202, false) // replay needs no live head
	if !s.Draining("base") {
		t.Fatal("lost drain on restart")
	}
	src.calls = nil
	src.missing = -1
	if _, err := s.Step(ctx, cfg, src); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(src.calls, []int64{101}) {
		t.Fatal("confirmation boundary", src.calls)
	}
	checkEnd(202, false)
	src.height = 120
	if _, err := s.Step(ctx, cfg, src); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !slices.Equal(src.calls, []int64{101, 104, 105}) {
		t.Fatal("final reads", src.calls)
	}
	checkEnd(200, true)
	if s.Watching("base") {
		t.Fatal("worker still active")
	}
	var rows int
	if err := s.db.QueryRow("SELECT (SELECT count(*) FROM cursors)+(SELECT count(*) FROM scan_gaps)+(SELECT count(*) FROM scan_edges)+(SELECT count(*) FROM closing_watches)").Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
	var seen []int64
	for {
		d, err := s.PendingNetwork(ctx, "pay", "base", 0)
		if err != nil {
			t.Fatal(err)
		}
		if d == nil {
			break
		}
		seen = append(seen, d.Activity.Height)
		if err = s.Ack(ctx, "pay", d.ID); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []int64{101, 102, 103, 104, 105}) {
		t.Fatal(seen)
	}
	request("GET", "/v1/events/next", "", 400)
	request("PUT", "/v2/subscriptions", "[]", 404)
}

func TestOverlappingConsumersAndReopeningDuringDrain(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := chains.Network{Name: "base"}
	src := &chain{height: 100}
	ConfigureNetworks(s, map[string]chains.Network{"base": cfg}, func(ctx context.Context, _ string) (int64, error) { return src.Head(ctx) })
	watches := []feed.Watch{{Network: "base", Address: wallet}}
	for _, c := range []string{"reopen", "leave", "stay"} {
		if _, err = s.Subscribe(ctx, c, watches); err != nil {
			t.Fatal(err)
		}
	}
	src.height = 102
	for _, c := range []string{"reopen", "leave"} {
		r, err := s.Subscribe(ctx, c, nil)
		if err != nil || len(r.Terminations) != 1 || r.Terminations[0].ToHeight != 102 || r.Terminations[0].Completed {
			t.Fatal(r, err)
		}
	}
	r, err := s.Subscribe(ctx, "reopen", watches)
	if err != nil || len(r.Registrations) != 1 || r.Registrations[0].FromHeight != 103 {
		t.Fatal(r, err)
	}
	src.height = 104
	if _, err = s.Step(ctx, cfg, src); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"reopen", "leave", "stay"} {
		var got []int64
		for {
			d, err := s.PendingNetwork(ctx, c, "base", 0)
			if err != nil {
				t.Fatal(err)
			}
			if d == nil {
				break
			}
			got = append(got, d.Activity.Height)
			if err = s.Ack(ctx, c, d.ID); err != nil {
				t.Fatal(err)
			}
		}
		want := []int64{101, 102, 103, 104}
		if c == "leave" {
			want = want[:2]
		}
		if !slices.Equal(got, want) {
			t.Fatal(c, got)
		}
	}
	r, err = s.Subscribe(ctx, "leave", nil)
	if err != nil || len(r.Terminations) != 1 || !r.Terminations[0].Completed || !s.Watching("base") {
		t.Fatal(r, err)
	}
}
