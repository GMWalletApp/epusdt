package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
	"github.com/GMWalletApp/ChainScanner/internal/testutil"
)

func TestNodeAdminLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chains.db")
	store, err := engine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	r, err := NewNodeRegistry(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	const admin = "a-dedicated-admin-token-123456"
	const consumer = "a-regular-consumer-token-123456"
	h, err := r.Handler(map[string]string{"pay": consumer}, admin)
	if err != nil {
		t.Fatal(err)
	}
	const route = "/v1/admin/nodes"
	req := func(method, path, key, body string, status int) []byte {
		t.Helper()
		rec := httptest.NewRecorder()
		in := httptest.NewRequest(method, path, strings.NewReader(body))
		in.Header.Set("Authorization", "Bearer "+key)
		h.ServeHTTP(rec, in)
		if rec.Code != status {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, status, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	req("GET", route, consumer, "", 401)
	req("POST", route+"/test", "", "", 401)
	// Remove Base defaults so the batch is deterministic and offline.
	defaults, err := r.List(ctx, "base")
	if err != nil || len(defaults) == 0 {
		t.Fatal(defaults, err)
	}
	for _, n := range defaults {
		req("DELETE", fmt.Sprintf("%s/%d", route, n.ID), admin, "", 204)
	}
	var wrong atomic.Bool
	rpc := testutil.RPCServer(t, func(v testutil.RPCRequest) (any, error) {
		switch v.Method {
		case "eth_chainId":
			if wrong.Load() {
				return "0x1", nil
			}
			return "0x2105", nil
		case "eth_blockNumber":
			return "0x2a", nil
		default:
			return nil, fmt.Errorf("unexpected method %s", v.Method)
		}
	})
	defer rpc.Close()
	body := fmt.Sprintf(`{"network":"base","url":%q,"protocol":"http"}`, rpc.URL)
	req("POST", route, admin, `{"network":"base","url":"file:///tmp/test"}`, 400)
	var n Node
	if err := json.Unmarshal(req("POST", route, admin, body, 201), &n); err != nil {
		t.Fatal(err)
	}
	if n.ID <= 0 || n.Status != "untested" {
		t.Fatal(n)
	}
	req("POST", route, admin, body, 409)
	var tested []Node
	if err = json.Unmarshal(req("POST", route+"/test?network=base", admin, "", 200), &tested); err != nil {
		t.Fatal(err)
	}
	if len(tested) != 1 || tested[0].Height == nil || *tested[0].Height != 42 || tested[0].CheckedAt == nil || tested[0].Status != "ok" {
		t.Fatal(tested)
	}
	id := fmt.Sprintf("%s/%d", route, n.ID)
	wrong.Store(true)
	if err = json.Unmarshal(req("POST", id+"/test", admin, "", 200), &n); err != nil {
		t.Fatal(err)
	}
	if n.Height != nil || n.Status != "unavailable" {
		t.Fatal(n)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = engine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err = NewNodeRegistry(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.List(ctx, "base")
	if err != nil || len(got) != 1 || got[0].ID != n.ID || got[0].Status != "unavailable" {
		t.Fatal(got, err)
	}
	h, err = r.Handler(nil, admin)
	if err != nil {
		t.Fatal(err)
	}
	req("DELETE", id, admin, "", 204)
	req("POST", id+"/test", admin, "", 404)
	req("DELETE", id, admin, "", 404)
	req("POST", route, admin, body, 201)
}

func TestNodePoolKeepsCursorOnProviderChange(t *testing.T) {
	ctx := context.Background()
	store, err := engine.Open(filepath.Join(t.TempDir(), "pool.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var failPrimary atomic.Bool
	var head atomic.Int64
	head.Store(42)
	server := func(allowedKey string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != allowedKey {
				t.Errorf("wrong credential on provider")
			}
			if allowedKey == "Bearer config-secret" && failPrimary.Load() {
				http.Error(w, "provider unavailable", 503)
				return
			}
			var req testutil.RPCRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				return
			}
			var result any
			switch req.Method {
			case "eth_chainId":
				result = "0x2105"
			case "eth_blockNumber":
				result = fmt.Sprintf("0x%x", head.Load())
			case "eth_getBlockByNumber":
				result = map[string]any{"number": req.Params[0], "hash": "0x123", "parentHash": "0x123", "timestamp": "0x10", "transactions": []any{}}
			case "eth_getBlockReceipts":
				result = []any{}
			default:
				t.Errorf("unexpected %s", req.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		}))
	}
	primary := server("Bearer config-secret")
	defer primary.Close()
	backup := server("Bearer backup-secret")
	defer backup.Close()
	settings := chains.Network{Name: "base", URL: primary.URL, APIKey: "config-secret"}
	r, err := NewNodeRegistry(ctx, store, []chains.Network{settings})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := r.List(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Origin == "builtin" {
			if err := r.Delete(ctx, n.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = store.Subscribe(ctx, "pay", []feed.Watch{{Network: "base", Address: wallet}}); err != nil {
		t.Fatal(err)
	}
	p := newRegistryPool(ctx, r, settings)
	defer p.Close()
	if _, err = store.Step(ctx, settings, p); err != nil {
		t.Fatal(err)
	}
	added, err := r.Add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"base", "http", backup.URL}, APIKey: "backup-secret"})
	if err != nil {
		t.Fatal(err)
	}
	failPrimary.Store(true)
	if _, err = store.Step(ctx, settings, p); err != nil {
		t.Fatal("backup should serve the same step", err)
	}
	head.Store(43)
	if _, err = store.Step(ctx, settings, p); err != nil {
		t.Fatal(err)
	}
	failPrimary.Store(false)
	if err = r.Delete(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	head.Store(44)
	if _, err = store.Step(ctx, settings, p); err != nil {
		t.Fatal(err)
	}
	h, err := store.Height(ctx, "base")
	if h != 44 || err != nil {
		t.Fatal(h, err)
	}
}

const wallet = "0x1111111111111111111111111111111111111111"

func TestConcreteEVMNetworkDemand(t *testing.T) {
	s, e := engine.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var baseCalls, ethCalls, tip atomic.Int64
	tip.Store(50)
	node := func(id string, calls *atomic.Int64) func(testutil.RPCRequest) (any, error) {
		return func(r testutil.RPCRequest) (any, error) {
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
	eth := testutil.RPCServer(t, node("0x1", &ethCalls))
	defer eth.Close()
	base := testutil.RPCServer(t, node("0x2105", &baseCalls))
	defer base.Close()
	ns := []chains.Network{{Name: "ethereum", URL: eth.URL}, {Name: "base", URL: base.URL}}
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
		if _, e = s.Subscribe(ctx, "a", []feed.Watch{{Network: name, Address: wallet}}); e == nil {
			t.Fatal("accepted missing/unconfigured chain", name)
		}
	}
	if baseCalls.Load()+ethCalls.Load() != 0 {
		t.Fatal("called idle node")
	}
	baseWatch := []feed.Watch{{Network: "base", Address: wallet}}
	reg, e := s.Subscribe(ctx, "a", baseWatch)
	if e != nil || len(reg.Registrations) != 1 || reg.Registrations[0].FromHeight != 51 {
		t.Fatal(reg, e)
	}
	if _, e = s.Subscribe(ctx, "b", baseWatch); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Subscribe(ctx, "a", nil); e != nil || !s.Watching("base") {
		t.Fatal("lost second consumer", e)
	}
	if ethCalls.Load() != 0 || s.Watching("ethereum") {
		t.Fatal("Base registration woke Ethereum")
	}
	if _, e = s.Subscribe(ctx, "b", nil); e != nil {
		t.Fatal(e)
	}
	if h, _ := s.Height(ctx, "base"); h != -1 || s.Watching("base") {
		t.Fatal("idle cursor", h)
	}
	tip.Store(500)
	reg, e = s.Subscribe(ctx, "b", baseWatch)
	if e != nil || reg.Registrations[0].FromHeight != 501 {
		t.Fatal(reg, e)
	}
	if _, e = s.Subscribe(ctx, "a", []feed.Watch{{Network: "ethereum", Address: wallet}}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Subscribe(ctx, "b", nil); e != nil || !s.Watching("ethereum") || s.Watching("base") {
		t.Fatal("mixed EVM chains", e)
	}
}
