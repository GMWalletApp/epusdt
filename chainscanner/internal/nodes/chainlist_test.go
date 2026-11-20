package nodes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
        {"url":"https://slow.example"},{"url":"wss://stream.example"},
        {"url":"https://mainnet.base.org"},{"url":"https://operator.example"},
        {"url":"https://requires.example/${API_KEY}"}]},
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
	payload.Store(`[{"chainId":8453,"rpc":[{"url":"https://slow.example"},{"url":"wss://stream.example"}]}]`)
	offline.Store(true)
	refresh(0)
	nodes = inventory()
	if nodes[good.URL].ID != 0 || nodes[custom.URL].ID != custom.ID || nodes["https://mainnet.base.org"].Origin != "builtin" {
		t.Fatal(nodes)
	}
	offline.Store(false)
	refresh(1)
	payload.Store(`[{"chainId":8453,"rpc":[{"url":"https://mainnet.base.org"}]}]`)
	refresh(0)
	if inventory()[good.URL].ID != 0 {
		t.Fatal("upstream removal was ignored")
	}
}
