package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/tidwall/gjson"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestFallbackSelectionThroughAdminAndStrictAPI(t *testing.T) {
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
	call := func(method, path, body, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	for _, host := range []string{"regular.example", "paid-one.example", "paid-two.example"} {
		path := "/v1/admin/nodes"
		if host != "regular.example" {
			path += "/fallback"
		}
		call("POST", path, `{"network":"base","url":"https://`+host+`","protocol":"http"}`, admin, 201)
	}
	r.lookup.probe = func(context.Context, chains.Network, bool) error { return nil }
	sample, err := os.ReadFile("../chains/testdata/strict/evm.json")
	if err != nil {
		t.Fatal(err)
	}
	hash := gjson.GetBytes(sample, "transaction.hash").Str
	var mu sync.Mutex
	var calls []string
	var fallbackErr error
	var fallbackBody json.RawMessage = sample
	r.lookup.read = func(_ context.Context, n chains.Network, h string, _ bool) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		if h != hash {
			t.Errorf("unexpected hash %s", h)
		}
		calls = append(calls, n.URL)
		if strings.Contains(n.URL, "paid-") {
			return fallbackBody, fallbackErr
		}
		return sample, nil
	}
	path := "/v1/transactions/base/" + hash
	check := func(suffix string, wantCode, wantCalls int) {
		t.Helper()
		calls = nil
		w := call("GET", path+suffix, "", client, wantCode)
		if len(calls) != wantCalls {
			t.Fatalf("%s: calls %v, response %s", suffix, calls, w.Body.String())
		}
		if w.Code == 200 && gjson.Get(w.Body.String(), "result.transaction.hash").Str != hash {
			t.Fatal("wrong transaction", w.Body.String())
		}
	}
	check("", 200, 1)
	check("?fallback_only=true", 200, 1)
	if !strings.Contains(calls[0], "paid-") {
		t.Fatal(calls)
	}
	call("PUT", "/v1/admin/blacklist/keyword:paid-two", "", admin, 204)
	check("?fallback_only=true", 200, 1)
	// A pending transaction is retryable.
	fallbackBody = json.RawMessage(`{"receipt":null}`)
	check("?fallback_only=true", 502, 1)
	// The next regular endpoint can verify after a retryable fallback failure.
	call("POST", "/v1/admin/nodes", `{"network":"base","url":"https://regular-two.example","protocol":"http"}`, admin, 201)
	for _, tc := range []struct {
		raw json.RawMessage
		err error
	}{
		{fallbackBody, nil}, {json.RawMessage(`{}`), nil}, {nil, errors.New("upstream unavailable")},
	} {
		fallbackBody, fallbackErr = tc.raw, tc.err
		check("", 200, 2)
		if !strings.Contains(calls[0], "paid-one") || !strings.Contains(calls[1], "regular") {
			t.Fatal(calls)
		}
	}
	// An observed transaction must exclude its original source, including a fallback.
	fallbackBody, fallbackErr = sample, nil
	id := chains.SourceIDs(chains.Network{Name: "base", URL: "https://paid-one.example/other-key"})[0]
	if _, err := r.db.Exec("INSERT INTO transaction_sources VALUES(?,?,?)", "base", hash, id); err != nil {
		t.Fatal(err)
	}
	check("?observed=true", 200, 1)
	if !strings.Contains(calls[0], "regular") {
		t.Fatal(calls)
	}
	check("?fallback_only=true&observed=true", 503, 0)
	call("GET", path+"/strict", "", client, 404)

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

func TestTONFallbackIndexEligibleWithoutPublicLocator(t *testing.T) {
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
	nodes, err := r.lookupCandidates(ctx, "ton", false)
	if err != nil {
		t.Fatal(err)
	}
	r.lookup.probe = func(context.Context, chains.Network, bool) error { return nil }
	for _, c := range nodes {
		if c.fallback && c.cfg.Protocol == "toncenter-v3" {
			return
		}
	}
	t.Fatal("no configured TON index available for transaction lookup")
}
