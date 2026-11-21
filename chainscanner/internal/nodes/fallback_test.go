package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
	"github.com/GMWalletApp/ChainScanner/internal/testutil"
)

func TestFallbackRepairsFailedBlocksAndResumesAfterRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var head atomic.Int64
	head.Store(40)
	var paidDown atomic.Bool
	var ordinaryDown atomic.Bool
	var paidCalls atomic.Int32
	var mu sync.Mutex
	var paidBlocks []string
	server := func(paid bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if paid {
				paidCalls.Add(1)
				if r.Header.Get("Authorization") != "Bearer paid-token" {
					t.Error("missing paid credential")
				}
			} else if ordinaryDown.Load() {
				http.Error(w, "offline", 503)
				return
			}
			var q testutil.RPCRequest
			if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
				t.Error(err)
				return
			}
			var value any
			switch q.Method {
			case "eth_chainId":
				value = "0x2105"
			case "eth_blockNumber":
				value = fmt.Sprintf("0x%x", head.Load())
			case "eth_getBlockByNumber":
				var height string
				if err := json.Unmarshal(q.Params[0], &height); err != nil {
					t.Error(err)
					return
				}
				if paid {
					mu.Lock()
					paidBlocks = append(paidBlocks, height)
					mu.Unlock()
				}
				if (!paid && (height == "0x2a" || height == "0x2c")) || (paid && paidDown.Load()) {
					http.Error(w, "busy", 429)
					return
				}
				value = map[string]any{"number": q.Params[0], "hash": "0x123", "parentHash": "0x123", "timestamp": "0x10", "transactions": []any{}}
			case "eth_getBlockReceipts":
				value = []any{}
			default:
				t.Errorf("unexpected RPC %s", q.Method)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": value})
		}))
	}
	ordinary, paid := server(false), server(true)
	defer ordinary.Close()
	defer paid.Close()
	path := filepath.Join(t.TempDir(), "scanner.sqlite")
	cfg := chains.Network{Name: "base", URL: ordinary.URL, IntervalSeconds: 1}
	var store *engine.Store
	var registry *NodeRegistry
	var pool *nodePool
	open := func() {
		t.Helper()
		var err error
		if store, err = engine.Open(path); err != nil {
			t.Fatal(err)
		}
		if registry, err = NewNodeRegistry(ctx, store, []chains.Network{cfg}); err != nil {
			t.Fatal(err)
		}
		pool = newRegistryPool(ctx, registry, cfg)
	}
	open()
	defer func() { pool.Close(); store.Close() }()
	existing, err := registry.List(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range existing {
		if n.Origin == "builtin" {
			if err := registry.Delete(ctx, n.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	const admin = "secret-admin-0123456789012345"
	const user = "secret-user-01234567890123456"
	handler, err := registry.Handler(map[string]string{"merchant": user}, admin)
	if err != nil {
		t.Fatal(err)
	}
	add := func(route, token, body string, code int) []byte {
		t.Helper()
		r := httptest.NewRequest("POST", route, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("HTTP %d, want %d: %s", w.Code, code, w.Body)
		}
		return w.Body.Bytes()
	}
	const route = "/v1/admin/nodes/fallback"
	body := fmt.Sprintf(`{"network":"base","protocol":"http","url":%q,"api_key":"paid-token"}`, paid.URL)
	add(route, user, body, 401)
	add(route, admin, `{"network":"bitcoin","protocol":"unisat","url":"https://test.invalid"}`, 400)
	raw := add(route, admin, body, 201)
	var n Node
	if err = json.Unmarshal(raw, &n); err != nil || n.Origin != "fallback" || n.ID <= 0 || strings.Contains(string(raw), "paid-token") {
		t.Fatal("unexpected fallback receipt", n, err)
	}
	add(route, admin, body, 409)
	add("/v1/admin/nodes", admin, strings.Replace(body, paid.URL, paid.URL+"/", 1), 409)
	if _, err = store.Subscribe(ctx, "merchant", []feed.Watch{{Network: "base", Address: wallet}}); err != nil {
		t.Fatal(err)
	}
	pumpTo := func(height int64) {
		t.Helper()
		for ctx.Err() == nil {
			h, err := store.Height(ctx, "base")
			if err != nil {
				t.Fatal(err)
			}
			if h == height {
				return
			}
			more, err := store.Pump(ctx, cfg, pool)
			if err != nil {
				t.Fatal(err)
			}
			if !more {
				time.Sleep(10 * time.Millisecond)
			}
		}
		t.Fatal("height not reached", height)
	}
	head.Store(41)
	pumpTo(41)
	if paidCalls.Load() != 0 {
		t.Fatal("paid endpoint used for healthy block or height polling", paidCalls.Load())
	}
	head.Store(43)
	pumpTo(43)
	mu.Lock()
	first := slices.Clone(paidBlocks)
	mu.Unlock()
	if !slices.Equal(first, []string{"0x2a"}) {
		t.Fatal("unexpected paid reads", first)
	}
	// Both providers reject block 44. Preserve the gap, then restart with a
	// completely unavailable ordinary provider.
	paidDown.Store(true)
	head.Store(44)
	for ctx.Err() == nil {
		_, pumpErr := store.Pump(ctx, cfg, pool)
		last, err := store.LastFailedHeight(ctx, "base")
		if err != nil {
			t.Fatal(err)
		}
		if last == 44 {
			break
		}
		if pumpErr != nil {
			t.Fatal(pumpErr)
		}
	}
	if h, err := store.Height(ctx, "base"); err != nil || h != 43 {
		t.Fatal("skipped failed block", h, err)
	}
	pool.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	ordinaryDown.Store(true)
	paidDown.Store(false)
	head.Store(500)
	open()
	pumpTo(44)
	mu.Lock()
	all := slices.Clone(paidBlocks)
	mu.Unlock()
	if !slices.Equal(all, []string{"0x2a", "0x2c", "0x2c"}) {
		t.Fatal("paid source read outside gaps", all)
	}
}

func TestFallbackChecksConfirmationsAndCoolsAfterFailedRepairs(t *testing.T) {
	ctx := context.Background()
	p := NewSourcePool(ctx, "base")
	defer p.Close()
	cfg := []chains.Network{{Name: "base", URL: "https://ordinary.test", Confirmations: 3}, {Name: "base", URL: "https://paid.test", Confirmations: 3}}
	if err := p.SetNodes(cfg); err != nil {
		t.Fatal(err)
	}
	paid := p.nodes[1]
	paid.fallback = true
	now := time.Now()
	p.now = func() time.Time { return now }
	tip := int64(101)
	calls := 0
	reject := true
	p.open = func(_ context.Context, n chains.Network) (chains.Source, error) {
		if n == cfg[0] {
			return &mockLane{110, func(_ context.Context, _, end int64) (int64, []chains.Activity, error) { return end, nil, nil }}, nil
		}
		return &mockLane{tip, func(_ context.Context, _, end int64) (int64, []chains.Activity, error) {
			calls++
			if reject {
				return 0, nil, chains.ErrRateLimited
			}
			return end, nil, nil
		}}, nil
	}
	repair := func() {
		t.Helper()
		err := p.ScanPending(ctx, []chains.Span{{First: 100, Last: 100, Failed: true}}, func(_ chains.Span, _ chains.BlockLink, _ []chains.Activity, err error) error { return err })
		if err != nil {
			t.Fatal(err)
		}
	}
	repair() // 100 with three confirmations needs head 102.
	if calls != 0 || paid.readFailures != (failureStreak{}) {
		t.Fatal("charged an unconfirmed block or penalized a lagging provider")
	}
	tip = 102
	for range 3 {
		repair()
	}
	if calls != 3 || !paid.retry.Equal(now.Add(time.Minute)) {
		t.Fatal("paid circuit", calls, paid.retry)
	}
	repair()
	if calls != 3 {
		t.Fatal("called cooling paid endpoint")
	}
	now = now.Add(time.Minute)
	reject = false
	repair()
	if calls != 4 || !paid.retry.IsZero() {
		t.Fatal("paid node did not recover")
	}
}
