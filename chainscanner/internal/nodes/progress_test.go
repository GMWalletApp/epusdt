package nodes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

func TestWatchWorkerPublishesConfirmedProgress(t *testing.T) {
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int
			Method string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch req.Method {
		case "getSlot":
			result = 103
		case "getGenesisHash":
			result = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
		default:
			t.Errorf("unexpected RPC %s", req.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer rpc.Close()
	s, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	net := chains.Network{Name: "solana", URL: rpc.URL, Confirmations: 3}
	reg, err := NewNodeRegistry(ctx, s, []chains.Network{net})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := reg.List(ctx, "solana")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.URL != rpc.URL {
			if err := reg.Delete(ctx, n.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = s.Subscribe(ctx, "pay", []feed.Watch{{Network: "solana", Address: "11111111111111111111111111111111"}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); reg.Run(ctx, net) }()
	defer func() { cancel(); <-done }()
	for {
		rows, err := s.ScanProgress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		p := rows[0]
		if p.Status == "ok" {
			if *p.Target != 101 || *p.Cursor != 103 || *p.LagMS != 0 {
				t.Fatalf("incorrect progress: %+v", p)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker never published a height", p)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
