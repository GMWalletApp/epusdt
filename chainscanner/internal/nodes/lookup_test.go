package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestTransactionLookupRounds(t *testing.T) {
	const token = "a-unique-transaction-lookup-token"
	hash := "0x" + strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name               string
		strict             bool
		nodes, code, calls int
	}{
		{"ordinary", false, 6, 200, 1}, {"missing", false, 6, 404, 1}, {"agree with spare nodes", true, 6, 200, 2},
		{"retry", true, 6, 200, 4}, {"busy retry", true, 6, 200, 4}, {"failed node", true, 6, 200, 4}, {"both disagree", true, 4, 502, 4},
		{"all missing", true, 6, 502, 4}, {"only two", true, 2, 200, 2}, {"short first group", true, 1, 503, 0}, {"short second group", true, 3, 503, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			s, e := engine.Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			r, e := NewNodeRegistry(ctx, s, []chains.Network{{Name: "base"}})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = r.db.Exec("UPDATE rpc_nodes SET removed=1"); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < tc.nodes; i++ {
				_, e = r.Add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"base", "http", fmt.Sprintf("https://p%d.example", i)}})
				if e != nil {
					t.Fatal(e)
				}
			}
			if tc.name == "busy retry" {
				for i := 0; i < 4; i++ {
					defer r.lookup.lanes.scan(providerKey(chains.Network{Name: "base", URL: fmt.Sprintf("https://p%d.example", i)}))()
				}
			}
			var mu sync.Mutex
			seen := map[string]bool{}
			calls := 0
			barrier := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
			r.lookup.probe = func(context.Context, chains.Network, bool) error { return nil }
			r.lookup.read = func(ctx context.Context, n chains.Network, h string, public bool) (json.RawMessage, error) {
				mu.Lock()
				if seen[n.URL] || h != hash || public {
					t.Errorf("unexpected request to %s for %s", n.URL, h)
				}
				seen[n.URL] = true
				calls++
				idx := calls
				mu.Unlock()
				if tc.strict {
					group := (idx - 1) / 2
					if idx%2 == 0 {
						close(barrier[group])
					}
					select {
					case <-barrier[group]:
					case <-time.After(2 * time.Second):
						t.Error("group did not run concurrently")
						return nil, ctx.Err()
					}
				}
				if tc.name == "missing" || tc.name == "all missing" || tc.name == "failed node" && idx == 1 {
					return nil, chains.ErrTransactionMissing
				}
				if (tc.name == "both disagree" || idx <= 2 && (tc.name == "retry" || tc.name == "busy retry" || tc.name == "short second group")) && idx%2 == 1 {
					return json.RawMessage(`{"amount":9007199254740993}`), nil
				}
				return json.RawMessage(`{"amount":9007199254740992}`), nil
			}
			h, e := r.Handler(map[string]string{"alice": token}, "")
			if e != nil {
				t.Fatal(e)
			}
			path := "/v1/transactions/base/" + hash
			if tc.strict {
				path += "/strict"
			}
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			w = httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.code || calls != tc.calls {
				t.Fatalf("%d, calls %d, %s", w.Code, calls, w.Body)
			}
			if tc.code == 200 && !strings.Contains(w.Body.String(), `"amount":9007199254740992`) {
				t.Fatal(w.Body)
			}
			if tc.code != 200 && strings.Contains(w.Body.String(), "amount") {
				t.Fatal("unverified data escaped")
			}
		})
	}
}

func TestLookupExcludesBusyProvidersAndReleasesPartialGroups(t *testing.T) {
	var l transactionLookup
	key := func(raw string) string { return providerKey(chains.Network{Name: "base", URL: raw}) }
	a, b, c := key("https://gateway.example/key1"), key("wss://GATEWAY.example:443/key2"), key("https://other.example")
	if a != b {
		t.Fatal("aliases counted as independent")
	}
	candidates := []lookupCandidate{{key: a, healthy: true}, {key: b, healthy: true}, {key: c, healthy: true}}
	release := l.lanes.scan(a)
	if g := l.group(t.Context(), candidates[:2], map[string]bool{}, 2); len(g) != 0 {
		t.Fatal("aliases supplied a quorum")
	}
	g := l.group(t.Context(), candidates, map[string]bool{}, 1)
	if len(g) != 1 || g[0].key != c {
		t.Fatal("leaked partial reservation")
	}
	secondScan := l.lanes.scan(c)
	secondScan()
	g[0].release()
	release()
	if g = l.group(t.Context(), candidates, map[string]bool{}, 2); len(g) != 2 || g[0].key == g[1].key {
		t.Fatal("expected two distinct providers")
	}
	for _, lease := range g {
		lease.release()
	}
}

func TestLookupCooldown(t *testing.T) {
	var l transactionLookup
	v := nodeVersion{id: 1}
	for range 3 {
		l.record(v, context.DeadlineExceeded)
	}
	now := time.Now()
	if l.ready(v, now) || !l.ready(v, now.Add(61*time.Second)) {
		t.Fatal("expected one minute cooldown")
	}
	for range 3 {
		l.record(v, context.DeadlineExceeded)
	}
	if l.ready(v, now.Add(61*time.Second)) {
		t.Fatal("expected escalated cooldown")
	}
	l.record(v, chains.ErrTransactionMissing)
	if !l.ready(v, now) {
		t.Fatal("valid response did not recover availability")
	}
}

func TestStrictLookupTopsUpOnlyTheMissingSlots(t *testing.T) {
	var l transactionLookup
	candidates := []lookupCandidate{{key: "busy", healthy: true}, {key: "idle1", healthy: true}, {key: "idle2", healthy: true}}
	scan := l.lanes.scan("busy")
	defer scan()
	g := l.group(t.Context(), candidates, map[string]bool{}, 2)
	if len(g) != 2 {
		t.Fatalf("got %d idle providers", len(g))
	}
	for _, v := range g {
		if v.key == "busy" {
			t.Fatal("borrowed unnecessarily")
		}
		v.release()
	}
	otherScan := l.lanes.scan("idle2")
	defer otherScan()
	g = l.group(t.Context(), candidates, map[string]bool{}, 2)
	if len(g) != 2 || g[0].key != "idle1" || g[1].key != "busy" {
		t.Fatal("expected one idle plus one busy", g)
	}
	if _, ok := l.lanes.tryLookup("busy", true); ok {
		t.Fatal("more than one supplemental query")
	}
	if ordinary := l.group(t.Context(), candidates, map[string]bool{}, 1); len(ordinary) != 0 {
		t.Fatal("ordinary lookup borrowed capacity")
	}
	for _, v := range g {
		v.release()
	}
}

func TestUnreachableIdleNodeDoesNotReceiveHash(t *testing.T) {
	l := transactionLookup{probe: func(ctx context.Context, cfg chains.Network, _ bool) error {
		if cfg.URL == "offline" {
			return context.DeadlineExceeded
		}
		return nil
	}}
	candidates := []lookupCandidate{{key: "a", cfg: chains.Network{URL: "offline"}}, {key: "b", cfg: chains.Network{URL: "online"}}}
	group := l.group(t.Context(), candidates, map[string]bool{}, 1)
	if len(group) != 1 || group[0].key != "b" {
		t.Fatal(group)
	}
	group[0].release()
}
