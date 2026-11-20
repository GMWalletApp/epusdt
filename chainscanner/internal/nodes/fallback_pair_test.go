package nodes

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestFallbackPairsThroughAdminAndStrictAPI(t *testing.T) {
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := NewNodeRegistry(t.Context(), store, []chains.Network{{Name: "base"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec("UPDATE rpc_nodes SET removed=1"); err != nil {
		t.Fatal(err)
	}
	const admin = "an-admin-token-for-fallback-test"
	const client = "a-consumer-token-for-fallback-test"
	handler, err := r.Handler(map[string]string{"app": client}, admin)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string, status int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
	}
	for _, host := range []string{"regular.example", "paid-one.example", "paid-two.example"} {
		path := "/v1/admin/nodes"
		if host != "regular.example" {
			path += "/fallback"
		}
		call("POST", path, `{"network":"base","url":"https://`+host+`","protocol":"http"}`, admin, 201)
	}
	r.lookup.probe = func(context.Context, chains.Network, bool) error { return nil }
	var mu sync.Mutex
	calls := map[string]int{}
	r.lookup.read = func(_ context.Context, n chains.Network, _ string, _ bool) (json.RawMessage, error) {
		mu.Lock()
		calls[n.URL]++
		mu.Unlock()
		return json.RawMessage(`{"tx":"ok"}`), nil
	}
	path := "/v1/transactions/base/0x" + strings.Repeat("ab", 32) + "/strict"
	call("GET", path, "", client, 200)
	call("GET", path+"?fallback_only=true", "", client, 200)
	if len(calls) != 2 || calls["https://paid-one.example"] != 2 || calls["https://paid-two.example"] != 2 {
		t.Fatal(calls)
	}
	call("PUT", "/v1/admin/blacklist/keyword:paid-two", "", admin, 204)
	call("GET", path+"?fallback_only=true", "", client, 503)
	call("GET", path, "", client, 200)
	if calls["https://regular.example"] != 1 || calls["https://paid-two.example"] != 2 {
		t.Fatal(calls)
	}
}

func TestRepairsRotateIndependentlyOfFreshBlocks(t *testing.T) {
	p := &SourcePool{now: time.Now}
	for _, role := range []bool{false, true, true} {
		p.nodes = append(p.nodes, &endpoint{fallback: role, tip: 100})
	}
	eligible := func(*endpoint) bool { return false }
	for i := range 6 {
		ordinary, _ := p.pickRange(chains.Span{Last: 1}, 0, nil, eligible)
		repair, _ := p.pickRange(chains.Span{Last: 1, Failed: true}, 0, nil, eligible)
		if ordinary != p.nodes[0] || repair != p.nodes[1+i%2] {
			t.Fatalf("unexpected assignment on repair %d", i)
		}
	}
}

func TestTONFallbackIndexesVoteWithoutPublicLocator(t *testing.T) {
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	r, err := NewNodeRegistry(ctx, store, []chains.Network{{Name: "ton"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"a.example", "b.example"} {
		n, err := r.add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"ton", "toncenter-v3", "https://" + host}}, true)
		if err != nil || n.Scannable {
			t.Fatal(n, err)
		}
	}
	nodes, err := r.lookupCandidates(ctx, "ton", true)
	if err != nil {
		t.Fatal(err)
	}
	r.lookup.probe = func(context.Context, chains.Network, bool) error { return nil }
	group, _ := r.tonGroup(ctx, nodes, map[string]bool{}, "unused-hash")
	if len(group) != 2 {
		t.Fatal("missing funded TON pair")
	}
	for _, v := range group {
		v.release()
		if !v.fallback || v.cfg.Protocol != "toncenter-v3" {
			t.Fatal(v.cfg)
		}
	}
}
