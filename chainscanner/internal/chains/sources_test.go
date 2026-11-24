package chains

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/testutil"
)

func TestEVMBlockReceiptsPreserveAddressActivity(t *testing.T) {
	contract := "0x" + strings.Repeat("2", 40)
	rpc := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "eth_chainId":
			return "0x89", nil
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
	rpc := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "getGenesisHash":
			return "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d", nil
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
func TestNewEVMRejectsWrongChain(t *testing.T) {
	srv := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		if r.Method != "eth_chainId" {
			t.Error(r.Method)
		}
		return "0x1", nil
	})
	defer srv.Close()
	for _, name := range []string{"arbitrum", "optimism", "base", "hyperevm", "monad", "arc", "avalanche"} {
		src, err := NewSource(context.Background(), Network{Name: name, URL: srv.URL})
		if err == nil {
			src.Close()
			t.Fatal(name, "accepted the Ethereum endpoint")
		}
	}
}

func TestNewSolanaValidatesMainnet(t *testing.T) {
	const mainnet = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
	for _, genesis := range []string{mainnet, "wrong-genesis", ""} {
		t.Run(genesis, func(t *testing.T) {
			srv := testutil.RPCServer(t, func(req testutil.RPCRequest) (any, error) {
				if req.Method == "getGenesisHash" {
					return genesis, nil
				}
				return 42, nil
			})
			defer srv.Close()
			src, err := NewSource(t.Context(), Network{Name: "solana", URL: srv.URL})
			if src != nil {
				defer src.Close()
			}
			if (err == nil) != (genesis == mainnet) {
				t.Fatalf("genesis=%q err=%v", genesis, err)
			}
		})
	}
}

const wallet = "0x1111111111111111111111111111111111111111"
