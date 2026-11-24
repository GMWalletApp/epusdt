package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestBuiltinRoutesAndHeightOnlyNodes(t *testing.T) {
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
	list, err := r.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[NodeEndpoint]bool{}
	for _, n := range list {
		key := n.NodeEndpoint.identity()
		if seen[key] {
			t.Fatalf("duplicate %v", n.NodeEndpoint)
		}
		seen[key] = true
		if n.Scannable {
			if err := chains.ValidateNetwork(r.network(n, chains.Network{})); err != nil {
				t.Fatal(n.NodeEndpoint, err)
			}
		}
	}
	for _, e := range []NodeEndpoint{
		{"solana", "http", "https://api.mainnet-beta.solana.com"},
		{"solana", "http", "https://solana-rpc.publicnode.com"},
		{"solana", "http", "https://public.rpc.solanavibestation.com"},
		{"solana", "ws", "wss://solana-rpc.publicnode.com"},
	} {
		if !seen[e] {
			t.Fatal("missing", e)
		}
	}
	for _, e := range heightOnlyNodes {
		if !seen[e] {
			t.Fatal("missing", e)
		}
		pool := newRegistryPool(ctx, r, chains.Network{Name: e.Network})
		err := pool.refresh(ctx)
		pool.Close()
		if err != nil {
			t.Fatal(err)
		}
		for cfg := range pool.inventory {
			if cfg.URL == e.URL {
				t.Fatal("read lane for height-only node", cfg)
			}
		}
	}
	for _, n := range list {
		if n.Network == "bitcoin" && n.Scannable != (n.Protocol == "jsonrpc" && !strings.Contains(n.URL, "rpcnode.dev")) {
			t.Fatal(n.NodeEndpoint)
		}
		if strings.Contains(n.URL, "yellowstone-grpc.publicnode.com") {
			t.Fatal("token required", n.NodeEndpoint)
		}
	}
}

// Check the source list before SQLite's unique constraint can hide repeated entries.
func TestBuiltinCatalogIsUnique(t *testing.T) {
	seen := map[NodeEndpoint]NodeEndpoint{}
	for _, e := range builtinNodes {
		key := e.identity()
		if original, ok := seen[key]; ok {
			t.Errorf("%v duplicates %v", e, original)
		}
		seen[key] = e
	}
}

func TestAptosLabsHostsUseSeparateReadLanes(t *testing.T) {
	pool := NewSourcePool(context.Background(), "aptos")
	defer pool.Close()
	err := pool.SetNodes([]chains.Network{
		{Name: "aptos", URL: "https://api.mainnet.aptoslabs.com/v1"},
		{Name: "aptos", URL: "https://fullnode.mainnet.aptoslabs.com/v1"},
		{Name: "aptos", URL: "https://api.mainnet.aptoslabs.com:443/v1/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.nodes) != 2 || pool.nodes[0].historyKey == pool.nodes[1].historyKey {
		t.Fatal("expected two distinct Aptos Labs read lanes")
	}
}

func TestAvalancheBuiltinSharesHistoryAndLookupVote(t *testing.T) {
	ws := NodeEndpoint{"avalanche", "ws", "wss://api.avax.network/ext/bc/C/ws"}
	history := chains.Network{Name: ws.Network, Protocol: "http", URL: "https://api.avax.network/ext/bc/C/rpc"}
	pair := chains.Network{Name: ws.Network, Protocol: ws.Protocol, URL: ws.URL, HistoryURL: builtinHistory[ws]}
	pool := NewSourcePool(t.Context(), ws.Network)
	defer pool.Close()
	if err := pool.SetNodes([]chains.Network{pair, history}); err != nil {
		t.Fatal(err)
	}
	if len(pool.nodes) != 2 || pool.nodes[0].historyKey != pool.nodes[1].historyKey {
		t.Fatal("Avalanche WS and HTTP must share one history lane")
	}
	lookup, ok := chains.LookupRoute(pair)
	if !ok || lookup != history || chains.ProviderKey(pair) != chains.ProviderKey(history) {
		t.Fatal("expected one lookup provider", lookup)
	}
}

func TestPublicNodeHistoryLane(t *testing.T) {
	if historyLane("base", "wss://base-rpc.publicnode.com:443/") != historyLane("base", "https://base-rpc.publicnode.com") {
		t.Fatal("duplicate history lanes")
	}
	if historyLane("base", "wss://other.example") == historyLane("base", "https://other.example") {
		t.Fatal("changed custom routes")
	}
}

func TestBuiltinUpgradeReusesEquivalentRows(t *testing.T) {
	ctx := context.Background()
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = NewNodeRegistry(ctx, store, nil); err != nil {
		t.Fatal(err)
	}
	db := engine.Database(store)
	const base = "https://base-rpc.publicnode.com"
	var id int64
	if err = db.QueryRow(`INSERT INTO rpc_nodes(network,protocol,url,origin,api_key) VALUES('base','jsonrpc','https://BASE-RPC.PUBLICNODE.COM:443/','custom','operator-key') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// A pre-existing deletion with a different spelling must suppress the default.
	if _, err = db.Exec(`UPDATE rpc_nodes SET removed=1,url='wss://BASE-RPC.PUBLICNODE.COM:443/' WHERE network='base' AND url='wss://base-rpc.publicnode.com'`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		r, err := NewNodeRegistry(ctx, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := r.List(ctx, "base")
		if err != nil {
			t.Fatal(err)
		}
		var count int
		for _, row := range rows {
			switch row.NodeEndpoint.identity() {
			case (NodeEndpoint{"base", "http", base}):
				count++
				if row.ID != id || row.Origin != "custom" || row.options.APIKey != "operator-key" {
					t.Fatal("changed operator entry", row.ID, row.Origin)
				}
			case (NodeEndpoint{"base", "ws", "wss://base-rpc.publicnode.com"}):
				t.Fatal("resurrected deleted entry")
			}
		}
		if count != 1 {
			t.Fatal("HTTP count", count)
		}
		if _, err = r.Add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"base", "http", base}}); !errors.Is(err, errDuplicateNode) {
			t.Fatal("added alias", err)
		}
		if _, err = r.applyCatalog(ctx, "chainlist", "", []observation{{endpoint: NodeEndpoint{"base", "http", base}, height: 123}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetiredPublicRoutes(t *testing.T) {
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
	db := engine.Database(store)
	for e := range retiredEndpoints {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM rpc_nodes WHERE url=?", e.URL).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired endpoint seeded: %v, count=%d, err=%v", e, count, err)
		}
	}
	for _, row := range []struct{ url, origin string }{
		{"https://POLYGON-RPC.COM:443/", "builtin"},
		{"https://polygon.lava.build", "comparenodes"},
		{"https://web3.mybitkeep.vip", "custom"},
		{"https://geth.mybitkeep.vip", "fallback"},
		{"https://web3os.bitkeep.pro", "builtin"},
	} {
		network := "ethereum"
		if strings.Contains(strings.ToLower(row.url), "polygon") {
			network = "polygon"
		}
		if _, err := db.Exec("INSERT INTO rpc_nodes(network,protocol,url,origin) VALUES(?,?,?,?)", network, "http", row.url, row.origin); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		r, err = NewNodeRegistry(ctx, store, []chains.Network{{Name: "ethereum", URL: "https://web3os.bitkeep.pro"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, origin := range []string{"chainlist", "openchainbench", "comparenodes"} {
			candidates := []NodeEndpoint{{"polygon", "http", "https://POLYGON-RPC.COM:443/"}, {"polygon", "http", "https://polygon.lava.build"}}
			count, err := r.refreshCandidates(ctx, origin, "polygon", candidates, func(context.Context, chains.Network) (int64, error) {
				t.Error("probed retired endpoint")
				return 1, nil
			})
			if err != nil || count != 0 {
				t.Fatal(origin, count, err)
			}
		}
		rows, err := r.List(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		kept := 0
		for _, n := range rows {
			if retiredEndpoints[n.NodeEndpoint.identity()] {
				if n.Network != "ethereum" {
					t.Fatal("retired automatic node remains", n.URL)
				}
				kept++
			}
		}
		if kept != 3 {
			t.Fatalf("expected three operator-selected nodes, got %d", kept)
		}
	}
}
