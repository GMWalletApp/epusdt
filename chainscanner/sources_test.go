package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

type rpcRequest struct {
	ID     json.RawMessage
	Method string
	Params []json.RawMessage
}

func rpcFixture(t *testing.T, reply func(rpcRequest) (any, error)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		value, err := reply(req)
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if err != nil {
			response["error"] = map[string]any{"code": -32000, "message": err.Error()}
		} else {
			response["result"] = value
		}
		json.NewEncoder(w).Encode(response)
	}))
}
func TestEVMBlockReceiptsPreserveAddressActivity(t *testing.T) {
	contract := "0x" + strings.Repeat("2", 40)
	rpc := rpcFixture(t, func(r rpcRequest) (any, error) {
		switch r.Method {
		case "eth_getBlockByNumber":
			return map[string]any{"number": "0x1", "hash": "b", "timestamp": "0x2", "transactions": []any{map[string]string{"hash": "tx", "from": contract, "to": contract}}}, nil
		case "eth_getLogs":
			return nil, fmt.Errorf("filter unavailable")
		case "eth_getBlockReceipts":
			return []any{map[string]any{"blockHash": "b", "transactionHash": "tx", "logs": []any{map[string]any{"address": contract, "topics": []string{"topic", "0x" + strings.Repeat("0", 24) + wallet[2:]}}}}}, nil
		default:
			t.Error(r.Method)
			return nil, fmt.Errorf("unexpected method")
		}
	})
	defer rpc.Close()
	ctx := context.Background()
	s, err := NewSource(ctx, Network{Name: "polygon", URL: rpc.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	end, events, err := s.Read(ctx, 1, 1)
	if err != nil || end != 1 || len(events) != 1 || !slices.Contains(events[0].Addresses, wallet) {
		t.Fatal(end, events, err)
	}
}
func TestSolanaSkippedSlotsAndSPLAccountOwner(t *testing.T) {
	owner := "So11111111111111111111111111111111111111112"
	rpc := rpcFixture(t, func(r rpcRequest) (any, error) {
		switch r.Method {
		case "getBlocks":
			return []int{101}, nil
		case "getFirstAvailableBlock":
			return 3, nil
		case "getBlock":
			var opts map[string]any
			if err := json.Unmarshal(r.Params[1], &opts); err != nil || opts["maxSupportedTransactionVersion"] != float64(1) {
				t.Fatal("version support", opts, err)
			}
			return map[string]any{"blockhash": "b", "blockTime": 10, "transactions": []any{map[string]any{"transaction": map[string]any{"signatures": []string{"s"}}, "meta": map[string]any{"postTokenBalances": []any{map[string]string{"owner": owner}}}}}}, nil
		default:
			t.Error(r.Method)
			return nil, fmt.Errorf("unexpected method")
		}
	})
	defer rpc.Close()
	ctx := context.Background()
	s, err := NewSource(ctx, Network{Name: "solana", URL: rpc.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	end, events, err := s.Read(ctx, 100, 103)
	if err != nil || end != 103 || len(events) != 1 || !slices.Contains(events[0].Addresses, owner) {
		t.Fatal(end, events, err)
	}
}
func TestTRONIncompleteReceiptsRetainCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wallet/getnowblock":
			fmt.Fprint(w, `{"block_header":{"raw_data":{"number":100}}}`)
		case "/wallet/getblockbynum":
			fmt.Fprint(w, `{"blockID":"b","block_header":{"raw_data":{"number":100}},"transactions":[{"txID":"t"}]}`)
		case "/wallet/gettransactioninfobyblocknum":
			fmt.Fprint(w, "[]")
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	n := Network{Name: "tron", URL: srv.URL}
	src, err := NewSource(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Step(ctx, n, src); err == nil {
		t.Fatal("accepted missing receipt")
	}
	h, err := s.Height(ctx, "tron")
	if err != nil || h != 99 {
		t.Fatal(h, err)
	}
}
