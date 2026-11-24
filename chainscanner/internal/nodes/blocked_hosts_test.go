package nodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestBlockedHostBoundaries(t *testing.T) {
	rules, err := parseBlockedHosts([]string{" Provider.Example. ", "2001:db8::1", "192.0.2.1", "keyword:Acme", "exact:only.example"})
	if err != nil {
		t.Fatal(err)
	}
	for url, want := range map[string]bool{
		"wss://provider.example:443/rpc":                  true,
		"grpcs://A.B.Provider.Example.:8080":              true,
		"https://[2001:0db8::1]:80":                       true,
		"adnl://192.0.2.1:22/key":                         true,
		"https://notprovider.example/rpc":                 false,
		"https://provider.example.other/provider.example": false,
		"https://rpc-ACME-2.example":                      true,
		"https://clear.example/rpc-acme?key=acme":         false,
		"wss://only.example.:987":                         true,
		"https://sub.only.example":                        false,
		"ipc:///tmp/provider.example":                     false,
	} {
		if rules.matches(url) != want {
			t.Error(url, want)
		}
	}
	for _, invalid := range []string{"", "*.example", "https://example.com", "site:443", "a..b", "keyword:", "keyword:a*", "exact:"} {
		if _, err := parseBlockedHosts([]string{invalid}); err == nil {
			t.Error(invalid)
		}
	}
}

func TestBlockedHostsApplyBeforeAdmissionAndToSavedRoutes(t *testing.T) {
	ctx := t.Context()
	s, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := []chains.Network{{Name: "base"}}
	r, err := NewNodeRegistry(ctx, s, base)
	if err != nil {
		t.Fatal(err)
	}
	paired := NodeOptions{NodeEndpoint: NodeEndpoint{"solana", "ws", "wss://independent.example"}, HistoryURL: "https://history.blocked.example"}
	old, err := r.Add(ctx, paired)
	if err != nil {
		t.Fatal(err)
	}
	r, err = NewNodeRegistry(ctx, s, base, "blocked.example", "publicnode.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.get(ctx, old.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := r.add(ctx, paired, true); !errors.Is(err, errInvalidNode) {
		t.Fatal(err)
	}
	nodes, err := r.lookupCandidates(ctx, "base", false)
	if err != nil || len(nodes) == 0 {
		t.Fatal(err, nodes)
	}
	for _, node := range nodes {
		if r.blacklist.hosts().matches(node.cfg.URL) {
			t.Fatal(node.cfg.URL)
		}
	}
	bad := NodeEndpoint{"base", "http", "https://rpc.blocked.example"}
	_, err = r.refreshCandidates(ctx, "chainlist", "base", []NodeEndpoint{bad}, func(context.Context, chains.Network) (int64, error) {
		t.Error("blocked discovery host was probed")
		return 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err = NewNodeRegistry(ctx, s, []chains.Network{{Name: "base", URL: bad.URL}}, "blocked.example")
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.List(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range list {
		if node.URL == bad.URL {
			t.Fatal("blacklisted configured node visible")
		}
	}
}

func TestBlacklistAdminLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.db")
	ctx := t.Context()
	store, err := engine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	const admin = "a-dedicated-blacklist-admin-token"
	const user = "a-dedicated-blacklist-reader-token"
	open := func() (*NodeRegistry, http.Handler) {
		r, err := NewNodeRegistry(ctx, store, []chains.Network{{Name: "base"}}, "fixed.example")
		if err != nil {
			t.Fatal(err)
		}
		h, err := r.Handler(map[string]string{"client": user}, admin)
		if err != nil {
			t.Fatal(err)
		}
		return r, h
	}
	r, h := open()
	request := func(method, suffix, token string, want int) string {
		t.Helper()
		q := httptest.NewRequest(method, "/v1/admin/blacklist"+suffix, nil)
		q.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, q)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, suffix, w.Code, w.Body.String())
		}
		return strings.TrimSpace(w.Body.String())
	}
	provider := NodeOptions{NodeEndpoint: NodeEndpoint{"base", "http", "https://a.rpc.example"}}
	n, err := r.Add(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	request("PUT", "/rpc.example", user, 401)
	request("PUT", "/rpc.example:443", admin, 400)
	request("PUT", "/keyword:Rpc", admin, 204)
	request("PUT", "/exact:fixed.other", admin, 204)
	request("PUT", "/Rpc.Example.", admin, 204)
	request("PUT", "/rpc.example", admin, 204)
	request("DELETE", "/fixed.example", admin, 409)
	if _, err := r.get(ctx, n.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = engine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, h = open()
	want := `[{"host":"exact:fixed.other","source":"admin"},{"host":"fixed.example","source":"config"},{"host":"keyword:rpc","source":"admin"},{"host":"rpc.example","source":"admin"}]`
	if got := request("GET", "", admin, 200); got != want {
		t.Fatal(got)
	}
	if _, err := r.get(ctx, n.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	request("DELETE", "/rpc.example", admin, 204)
	request("DELETE", "/rpc.example", admin, 204)
	if _, err := r.get(ctx, n.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("keyword rule lost", err)
	}
	request("DELETE", "/keyword:rpc", admin, 204)
	if _, err := r.get(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
}

func TestStrictLookupDropsNewlyBlacklistedVote(t *testing.T) {
	ctx := t.Context()
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := NewNodeRegistry(ctx, store, []chains.Network{{Name: "base"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec("UPDATE rpc_nodes SET removed=1"); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"one.example"} {
		if _, err := r.Add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"base", "http", "https://" + host}}); err != nil {
			t.Fatal(err)
		}
	}
	r.lookup.probe = func(context.Context, chains.Network, bool) error { return nil }
	entered := make(chan struct{}, 2)
	resume := make(chan struct{})
	r.lookup.read = func(context.Context, chains.Network, string, bool) (json.RawMessage, error) {
		entered <- struct{}{}
		<-resume
		return nil, chains.ErrTransactionMissing
	}
	h := r.transactionHandler(map[string]chains.Network{"base": {Name: "base"}})
	query := func() int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/transactions/base/0x"+strings.Repeat("aa", 32), nil))
		return w.Code
	}
	result := make(chan int, 1)
	go func() { result <- query() }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	{
		select {
		case <-entered:
		case <-timer.C:
			close(resume)
			t.Fatal("read did not start")
		}
	}
	err = r.setBlocked(ctx, "one.example", true)
	close(resume)
	if err != nil {
		t.Fatal(err)
	}
	if status := <-result; status != 502 {
		t.Fatal(status)
	}
	if status := query(); status != 503 {
		t.Fatal(status)
	}
	if err := r.setBlocked(ctx, "one.example", false); err != nil {
		t.Fatal(err)
	}
	if status := query(); status != 404 {
		t.Fatal(status)
	}
}
