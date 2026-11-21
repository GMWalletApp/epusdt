package nodes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestChainListRefreshLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := NewNodeRegistry(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	custom, err := r.Add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"base", "http", "https://operator.example"}})
	if err != nil {
		t.Fatal(err)
	}
	var payload atomic.Value
	payload.Store(`[{"chainId":8453,"rpc":[
        {"url":"https://slow.example","tracking":"yes","score":0},
        {"url":"https://slow.example"},{"url":"https://SLOW.EXAMPLE:443/"},{"url":"wss://stream.example"},
        {"url":"https://MAINNET.BASE.ORG:443/"},{"url":"https://OPERATOR.example/"},{"url":"https://operator.example"},
        {"url":"https://requires.example/${API_KEY}"}]},
        {"chainId":2818,"rpc":[{"url":"https://retired.example"}]},
        {"chainId":84532,"rpc":[{"url":"https://testnet.example"}]}]`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, payload.Load().(string)) }))
	defer srv.Close()
	var probes atomic.Int32
	var offline atomic.Bool
	probe := func(ctx context.Context, n chains.Network) (int64, error) {
		probes.Add(1)
		if n.Name != "base" || (n.URL != "https://slow.example" && n.URL != "wss://stream.example") {
			t.Errorf("unexpected probe %+v", n)
		}
		if offline.Load() {
			return 0, errors.New("offline")
		}
		return 123, nil
	}
	refresh := func(want int) {
		t.Helper()
		got, err := r.refreshChainList(ctx, srv.Client(), srv.URL, probe)
		if err != nil || got != want {
			t.Fatalf("refresh=%d, %v; want %d", got, err, want)
		}
	}
	inventory := func() map[string]Node {
		t.Helper()
		all, err := r.List(ctx, "base")
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]Node{}
		for _, n := range all {
			found[n.URL] = n
		}
		return found
	}
	refresh(2)
	nodes := inventory()
	good := nodes["https://slow.example"]
	if good.ID == 0 || good.Status != "ok" || good.Origin != "chainlist" || good.Height == nil || *good.Height != 123 || probes.Load() != 2 {
		t.Fatal(good, probes.Load())
	}
	refresh(2)
	if inventory()[good.URL].ID != good.ID {
		t.Fatal("healthy node ID changed")
	}
	p := newRegistryPool(ctx, r, chains.Network{Name: "base"})
	defer p.Close()
	lookup := func(address string) *endpoint {
		t.Helper()
		if err := p.refresh(ctx); err != nil {
			t.Fatal(err)
		}
		for _, n := range p.nodes {
			if n.cfg.URL == address {
				return n
			}
		}
		return nil
	}
	old := lookup(good.URL)
	builtin := lookup("https://mainnet.base.org")
	for _, n := range []*endpoint{old, builtin} {
		if n == nil {
			t.Fatal("missing scan node")
		}
		for range 3 {
			p.record(n, true, errors.New("unavailable"))
		}
	}
	if err = r.Delete(ctx, nodes["wss://stream.example"].ID); err != nil {
		t.Fatal(err)
	}
	refresh(1)
	if probes.Load() != 5 {
		t.Fatal("deleted node was probed", probes.Load())
	}
	payload.Store("invalid")
	if _, err = r.refreshChainList(ctx, srv.Client(), srv.URL, probe); err == nil || inventory()[good.URL].ID != good.ID {
		t.Fatal("failed fetch replaced inventory", err)
	}
	payload.Store(`[{"chainId":8453,"rpc":[{"url":"https://slow.example"},{"url":"https://SLOW.EXAMPLE:443/"},{"url":"wss://stream.example"}]}]`)
	offline.Store(true)
	refresh(0)
	nodes = inventory()
	if nodes[good.URL].ID != 0 || nodes[custom.URL].ID != custom.ID || nodes["https://mainnet.base.org"].Origin != "builtin" {
		t.Fatal(nodes)
	}
	if lookup(good.URL) != nil || lookup(builtin.cfg.URL) != builtin || !time.Now().Before(builtin.retry) {
		t.Fatal("refresh did not preserve only the owned cooldown")
	}
	offline.Store(false)
	refresh(1)
	if newNode := lookup(good.URL); newNode == nil || newNode == old || !newNode.retry.IsZero() {
		t.Fatal("rediscovery inherited retired node penalty")
	}
	payload.Store(`[{"chainId":8453,"rpc":[{"url":"https://mainnet.base.org"}]}]`)
	refresh(0)
	if inventory()[good.URL].ID != 0 {
		t.Fatal("upstream removal was ignored")
	}
}

func TestDiscoveryOwnershipChangedDuringProbe(t *testing.T) {
	ctx := context.Background()
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := NewNodeRegistry(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"chainId":8453,"rpc":[{"url":"https://new.example"}]}]`)
	}))
	defer catalog.Close()
	count, err := r.refreshChainList(ctx, catalog.Client(), catalog.URL, func(context.Context, chains.Network) (int64, error) {
		_, err := r.Add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"base", "jsonrpc", "https://NEW.example:443/"}})
		return 123, err
	})
	if err != nil || count != 0 {
		t.Fatal("discovery claimed operator endpoint", count, err)
	}
}

func TestEndpointIdentityBoundaries(t *testing.T) {
	base := NodeEndpoint{"base", "http", "https://Example.com:443/"}
	if base.identity() != (NodeEndpoint{"base", "jsonrpc", "https://example.com"}).identity() {
		t.Fatal("root alias was not collapsed")
	}
	for _, other := range []NodeEndpoint{
		{"ethereum", "http", "https://example.com"},
		{"base", "ws", "wss://example.com"},
		{"base", "http", "https://example.com:8443"},
		{"base", "http", "https://example.com/path"},
		{"base", "http", "https://example.com/?key=abc"},
	} {
		if other.identity() == base.identity() {
			t.Fatal("merged independent identities", other)
		}
	}
}

func TestBackoffCapsAtOneHour(t *testing.T) {
	var streak failureStreak
	for i := 1; i <= 40; i++ {
		if streak.fail() != 0 || streak.fail() != 0 {
			t.Fatal("early pause", i)
		}
		if d := streak.fail(); d != time.Duration(min(i*(i+1)/2, 60))*time.Minute {
			t.Fatal("wrong duration", i, d)
		}
	}
}

func TestChainListQuarterHourSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, stop := context.WithCancel(context.Background())
		start := time.Now()
		var times []time.Duration
		runChainListSchedule(ctx, func(context.Context) {
			times = append(times, time.Since(start))
			if len(times) == 3 {
				stop()
			}
		})
		if len(times) != 3 || times[0] != 0 || times[1] != 15*time.Minute || times[2] != 30*time.Minute {
			t.Fatal(times)
		}
	})
}
