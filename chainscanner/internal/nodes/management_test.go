package nodes

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
	"github.com/GMWalletApp/ChainScanner/internal/testutil"
)

func TestManagedNodesStayOutOfSchedulingAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	ctx := t.Context()
	newRegistry := func() (*engine.Store, *NodeRegistry) {
		s, err := engine.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := NewNodeRegistry(ctx, s, []chains.Network{{Name: "base"}})
		if err != nil {
			t.Fatal(err)
		}
		return s, r
	}
	store, r := newRegistry()
	rows, _ := r.List(ctx, "base")
	for _, n := range rows {
		if err := r.Delete(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	add := func(url, purpose string, fallback bool) Node {
		t.Helper()
		n, err := r.add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"base", "http", url}, Purpose: purpose, Weight: 7, APIKey: "provider-key"}, fallback)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	normal := add("https://regular.example/tenant", "general", false)
	fallback := add("https://fallback.example/tenant", "both", true)
	manual := add("https://manual.example/tenant", "manual_verify", false)
	check := func(enabled bool) {
		t.Helper()
		pool := newRegistryPool(ctx, r, chains.Network{Name: "base"})
		defer pool.Close()
		if err := pool.refresh(ctx); err != nil {
			t.Fatal(err)
		}
		if enabled && len(pool.nodes) != 2 || !enabled && len(pool.nodes) != 0 {
			t.Fatalf("scan candidates: %d", len(pool.nodes))
		}
		if enabled && !slices.ContainsFunc(pool.nodes, func(n *endpoint) bool { return n.fallback && n.cfg.URL == fallback.URL && n.weight == 7 }) {
			t.Fatal("lost fallback role or weight")
		}
		auto, err := r.lookupCandidates(ctx, "base", false)
		if err != nil || enabled && len(auto) != 2 || !enabled && len(auto) != 0 {
			t.Fatalf("automatic voters: %d, %v", len(auto), err)
		}
		onDemand, err := r.lookupCandidates(ctx, "base", true)
		if err != nil || !slices.ContainsFunc(onDemand, func(c lookupCandidate) bool { return c.version.id == manual.ID }) {
			t.Fatal("manual voter missing", err)
		}
	}
	check(true)
	off := false
	for _, n := range []Node{normal, fallback} {
		if _, err := r.update(ctx, n.ID, NodePatch{Enabled: &off}); err != nil {
			t.Fatal(err)
		}
	}
	check(false)
	store.Close()
	store, r = newRegistry()
	defer store.Close()
	check(false)
	on := true
	for _, n := range []Node{normal, fallback} {
		updated, err := r.update(ctx, n.ID, NodePatch{Enabled: &on})
		if err != nil || updated.options.APIKey != "provider-key" || updated.Weight != 7 || updated.Role != n.Role || updated.Purpose != n.Purpose {
			t.Fatal("settings did not survive", err)
		}
	}
	check(true)
	newURL := "https://regular.example/other?project=2"
	edited, err := r.update(ctx, normal.ID, NodePatch{URL: &newURL})
	if err != nil || edited.URL != newURL || edited.ID != normal.ID {
		t.Fatal("edit", err)
	}
	if _, err := r.update(ctx, edited.ID, NodePatch{URL: &fallback.URL}); err != errDuplicateNode {
		t.Fatal("duplicate edit", err)
	}
	restored, err := r.update(ctx, edited.ID, NodePatch{URL: &normal.URL})
	if err != nil || restored.ID != normal.ID || restored.URL != normal.URL || restored.options.APIKey != normal.options.APIKey {
		t.Fatal("restore previous address", err)
	}
	if _, err := r.insert(ctx, NodeOptions{NodeEndpoint: edited.NodeEndpoint}, false, true); err != errDuplicateNode {
		t.Fatal("import resurrected the replaced address", err)
	}
	if err := r.Delete(ctx, edited.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.insert(ctx, NodeOptions{NodeEndpoint: normal.NodeEndpoint}, false, true); err != errDuplicateNode {
		t.Fatal("import resurrected edited URL", err)
	}
}

func TestHealthCheckBatchScopeAndDisabledDiscovery(t *testing.T) {
	ctx := t.Context()
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := NewNodeRegistry(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := r.List(ctx, "")
	for _, n := range all {
		if err := r.Delete(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	var calls [4]atomic.Int32
	for i, item := range []struct {
		network, purpose  string
		enabled, fallback bool
	}{{"base", "general", true, false}, {"base", "manual_verify", true, false}, {"ethereum", "general", true, true}, {"ethereum", "both", false, false}} {
		srv := testutil.RPCServer(t, func(q testutil.RPCRequest) (any, error) {
			calls[i].Add(1)
			switch q.Method {
			case "eth_chainId":
				if item.network == "base" {
					return "0x2105", nil
				}
				return "0x1", nil
			case "eth_blockNumber":
				return "0x10", nil
			}
			return nil, fmt.Errorf("unexpected method %s", q.Method)
		})
		defer srv.Close()
		_, err := r.add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{item.network, "http", srv.URL}, Purpose: item.purpose, Enabled: &item.enabled}, item.fallback)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = r.testAll(ctx, "base", false); err != nil || calls[0].Load() == 0 || calls[1].Load()+calls[2].Load()+calls[3].Load() != 0 {
		t.Fatal("per-chain scope", err)
	}
	if _, err = r.testAll(ctx, "", true); err != nil || calls[1].Load() == 0 || calls[2].Load() == 0 || calls[3].Load() != 0 {
		t.Fatal("global scope", err)
	}
	node := NodeEndpoint{"base", "http", "https://discovered.example"}
	if _, err = r.db.Exec("INSERT INTO rpc_nodes(network,protocol,url,origin) VALUES(?,?,?,'chainlist')", node.Network, node.Protocol, node.URL); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = r.db.QueryRow("SELECT id FROM rpc_nodes WHERE url=?", node.URL).Scan(&id); err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err = r.update(ctx, id, NodePatch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	_, err = r.refreshCandidates(ctx, "chainlist", "base", []NodeEndpoint{node}, func(context.Context, chains.Network) (int64, error) {
		t.Error("probed disabled discovery")
		return 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.get(ctx, id); err != nil || n.Enabled {
		t.Fatal("discovery lost disable", err)
	}
}
