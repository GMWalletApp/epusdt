package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
	"github.com/GMWalletApp/ChainScanner/internal/testutil"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestNonIndexedRewardAndAssetRefreshDelivery(t *testing.T) {
	const rewards = "0x2222222222222222222222222222222222222222"
	const token = "0xae7ab96520de3a18e5e111b5eaab095312d7fe84"
	const unrelated = "0x3333333333333333333333333333333333333333"
	const sig = "0xe2403640ba68fed3a2f88b7557551d1993f84b99bb10ff833f0cf8db0c5e0486"
	logs := []map[string]any{
		{"transactionHash": "tx", "blockHash": "block", "address": rewards, "topics": []string{sig},
			"data": "0x" + strings.Repeat("0", 24) + wallet[2:] + strings.Repeat("0", 24) + unrelated[2:]},
		{"transactionHash": "tx", "blockHash": "block", "address": token, "topics": []string{
			crypto.Keccak256Hash([]byte("TokenRebased(uint256,uint256,uint256,uint256,uint256,uint256,uint256)")).Hex(),
			fmt.Sprintf("0x%064x", 12345)}, "data": "0x" + strings.Repeat("0", 6*64)},
	}
	var blockReads, logReads atomic.Int32
	rpc := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "eth_chainId", "eth_blockNumber":
			return "0x1", nil
		case "eth_getBlockByNumber":
			blockReads.Add(1)
			return map[string]any{"number": "0x1", "hash": "block", "parentHash": "parent", "timestamp": "0x1", "transactions": []map[string]string{{"hash": "tx", "from": rewards, "to": rewards}}}, nil
		case "eth_getLogs":
			logReads.Add(1)
			return logs, nil
		default:
			t.Errorf("unexpected RPC %s", r.Method)
			return nil, fmt.Errorf("unexpected RPC")
		}
	})
	defer rpc.Close()
	var n chains.Network
	cfg := fmt.Sprintf(`{"name":"ethereum","url":%q,"asset_events":{"%s":{"%s":{"topics":1,"data_words":2,"address_words":[0]}}}}`, rpc.URL, rewards, sig)
	if err := json.Unmarshal([]byte(cfg), &n); err != nil {
		t.Fatal(err)
	}
	src, err := chains.NewSource(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.SeedCursor(t.Context(), n.Name, 0); err != nil {
		t.Fatal(err)
	}
	for consumer, address := range map[string]string{"reward-wallet": wallet, "token-monitor": token, "other-wallet": unrelated} {
		if _, err = store.Subscribe(t.Context(), consumer, []feed.Watch{{Network: n.Name, Address: address}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.Step(t.Context(), n, src); err != nil {
		t.Fatal(err)
	}
	if blockReads.Load() != 1 || logReads.Load() != 1 {
		t.Fatalf("one shared block should require one block read and one log read, got %d and %d", blockReads.Load(), logReads.Load())
	}
	for consumer, match := range map[string]string{"reward-wallet": wallet, "token-monitor": token, "other-wallet": ""} {
		d, err := store.PendingNetwork(t.Context(), consumer, n.Name, 0)
		if err != nil {
			t.Fatal(err)
		}
		if match == "" {
			if d != nil {
				t.Fatalf("amount mistaken for wallet: %+v", d)
			}
			continue
		}
		if d == nil || d.Activity.TxID != "tx" || !slices.Equal(d.Activity.Addresses, []string{match}) {
			t.Fatalf("delivery for %s: %+v", consumer, d)
		}
		var e chains.Evidence
		if err = json.Unmarshal(d.Activity.Data, &e); err != nil || len(e.Logs) != 2 || !slices.Equal(e.RefreshAssets, []string{token}) {
			t.Fatalf("missing evidence: %+v, %v", e, err)
		}
	}
}
