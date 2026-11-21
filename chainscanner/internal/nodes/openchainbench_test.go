package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestOpenChainBenchUsesPublicSpecWithJSONOrEventStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" && r.URL.Path == "/solana-rpc.yml" {
				fmt.Fprint(w, "slug: solana-rpc\nproviders:\n  - {slug: other, endpoint: 'https://b.example'}\n  - {slug: ranked, endpoint: 'https://a.example/'}\n  - {slug: duplicate, endpoint: 'https://a.example:443'}\n  - {slug: paid, tier: keyed, endpoint: 'https://paid.example'}\n")
				return
			}
			var req struct {
				Method string
				Params struct {
					Name      string
					Arguments struct{ Slug string }
				}
			}
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Method != "tools/call" || req.Params.Name != "get_benchmark" || req.Params.Arguments.Slug != "solana-rpc" {
				t.Error("wrong tool request")
				w.WriteHeader(400)
				return
			}
			msg := `{"id":1,"result":{"content":[{"type":"text","text":"{\"slug\":\"solana-rpc\",\"rankings\":[{\"slug\":\"ranked\"}]}"}]}}`
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, ": heartbeat\n\nevent: message\ndata: %s\n\n", msg)
			} else {
				fmt.Fprint(w, msg)
			}
		}))
		got, err := benchCandidates(context.Background(), srv.Client(), srv.URL, srv.URL+"/", "solana", "solana-rpc", "http")
		srv.Close()
		if err != nil || len(got) != 2 || got[0].URL != "https://a.example" || got[1].URL != "https://b.example" {
			t.Fatal(got, err)
		}
	}
}

func TestOpenChainBenchRefreshIsolation(t *testing.T) {
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
	observe := func(origin, net string, entries []NodeEndpoint, online bool) int {
		n, err := r.refreshCandidates(ctx, origin, net, entries, func(context.Context, chains.Network) (int64, error) {
			if online {
				return 123, nil
			}
			return 0, errors.New("offline")
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	row := func(e NodeEndpoint) Node {
		ns, err := r.List(ctx, e.Network)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range ns {
			if n.NodeEndpoint == e {
				return n
			}
		}
		return Node{}
	}
	a := NodeEndpoint{"aptos", "http", "https://apt.example"}
	s := NodeEndpoint{"solana", "http", "https://sol.example"}
	evm := NodeEndpoint{"base", "http", "https://base.example"}
	builtin := NodeEndpoint{"solana", "http", "https://solana-rpc.publicnode.com"}
	observe("chainlist", "", []NodeEndpoint{evm}, true)
	observe("openchainbench", "aptos", []NodeEndpoint{a}, true)
	if observe("openchainbench", "solana", []NodeEndpoint{s, builtin}, true) != 1 {
		t.Fatal("claimed builtin")
	}
	first := row(s).ID
	observe("openchainbench", "solana", []NodeEndpoint{s}, true)
	if first == 0 || row(s).ID != first {
		t.Fatal("lost active identity")
	}
	observe("openchainbench", "solana", []NodeEndpoint{s}, false)
	if row(s).ID != 0 || row(a).Origin != "openchainbench" || row(evm).Origin != "chainlist" || row(builtin).Origin != "builtin" {
		t.Fatal("bad retention on failed probe")
	}
	observe("openchainbench", "solana", []NodeEndpoint{s}, true)
	if row(s).ID == first || row(s).ID == 0 {
		t.Fatal("failed to rediscover")
	}
	if err = r.Delete(ctx, row(s).ID); err != nil {
		t.Fatal(err)
	}
	observe("openchainbench", "solana", []NodeEndpoint{s}, true)
	if row(s).ID != 0 {
		t.Fatal("resurrected operator deletion")
	}
	observe("openchainbench", "aptos", nil, true)
	if row(a).ID != 0 {
		t.Fatal("absent endpoint retained")
	}
}
