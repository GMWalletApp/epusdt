package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tidwall/gjson"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestTransactionLookupFailover(t *testing.T) {
	const token = "a-unique-transaction-lookup-token"
	sample, err := os.ReadFile("../chains/testdata/strict/evm.json")
	if err != nil {
		t.Fatal(err)
	}
	hash := gjson.GetBytes(sample, "transaction.hash").Str
	for _, tc := range []struct {
		name                           string
		nodes, failures, status, calls int
		failure                        error
		reason                         string
	}{
		{"rotate two endpoints", 2, 3, 200, 4, errors.New("transient"), ""},
		{"one available", 1, 0, 200, 1, nil, ""},
		{"missing", 3, 1, 404, 1, chains.ErrTransactionMissing, "not_found"},
		{"four failures then success", 5, 4, 200, 5, errors.New("upstream failure"), ""},
		{"exhausted", 4, 4, 502, 4, context.DeadlineExceeded, ""},
		{"pending", 4, 4, 502, 4, chains.ErrTransactionPending, "pending"},
		{"invalid evidence", 4, 4, 502, 4, nil, "incomplete"},
		{"unavailable", 0, 0, 503, 0, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			if _, err := r.db.Exec("UPDATE rpc_nodes SET removed=1"); err != nil {
				t.Fatal(err)
			}
			for i := range tc.nodes {
				if _, err := r.Add(ctx, NodeOptions{NodeEndpoint: NodeEndpoint{"base", "http", fmt.Sprintf("https://p%d.example", i)}}); err != nil {
					t.Fatal(err)
				}
			}
			r.lookup.probe = func(context.Context, chains.Network, bool) error { return nil }
			last := ""
			calls := 0
			r.lookup.read = func(_ context.Context, n chains.Network, h string, public bool) (json.RawMessage, error) {
				if last == n.URL || public || h != hash {
					t.Fatalf("unexpected read %s %s", n.URL, h)
				}
				last = n.URL
				calls++
				if calls <= tc.failures {
					return json.RawMessage(`{}`), tc.failure
				}
				return sample, nil
			}
			handler, err := r.Handler(map[string]string{"alice": token}, "")
			if err != nil {
				t.Fatal(err)
			}
			path := "/v1/transactions/base/" + hash
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.status || calls != tc.calls || w.Header().Get("X-ChainScanner-Evidence") != tc.reason {
				t.Fatal(w.Code, w.Header(), calls, w.Body)
			}
			if tc.status == 200 && gjson.Get(w.Body.String(), "result.transaction.hash").Str != hash {
				t.Fatal(w.Body)
			}
		})
	}
}

func TestLookupCooldown(t *testing.T) {
	var l transactionLookup
	v := nodeVersion{id: 1}
	for range 3 {
		l.record(v, context.DeadlineExceeded)
	}
	now := time.Now()
	if l.ready(v, now) || !l.ready(v, now.Add(61*time.Second)) {
		t.Fatal("expected one minute cooldown")
	}
	for range 3 {
		l.record(v, context.DeadlineExceeded)
	}
	if l.ready(v, now.Add(61*time.Second)) {
		t.Fatal("expected escalated cooldown")
	}
	l.record(v, chains.ErrTransactionMissing)
	if !l.ready(v, now) {
		t.Fatal("valid response did not recover availability")
	}
}

func TestOfficialGatewayVotes(t *testing.T) {
	for _, tc := range []struct{ chain, first, second string }{
		{"binance", "https://bsc-dataseed-public.bnbchain.org", "https://bsc-dataseed.binance.org"},
		{"monad", "wss://rpc3.monad.xyz", "https://rpc.ankr.com/monad_mainnet"},
		{"solana", "https://api.mainnet-beta.solana.com", "https://api.mainnet.solana.com"},
		{"aptos", "https://fullnode.mainnet.aptoslabs.com/v1", "https://api.mainnet.aptoslabs.com/v1"},
	} {
		if chains.ProviderKey(chains.Network{Name: tc.chain, URL: tc.first}) != chains.ProviderKey(chains.Network{Name: tc.chain, URL: tc.second}) {
			t.Errorf("same provider received two votes: %s and %s", tc.first, tc.second)
		}
	}
	if chains.ProviderKey(chains.Network{Name: "monad", URL: "https://rpc2.monad.xyz"}) == chains.ProviderKey(chains.Network{Name: "monad", URL: "wss://rpc3.monad.xyz"}) {
		t.Fatal("Goldsky and Ankr must be independent")
	}
}
