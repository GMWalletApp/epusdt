package task

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestListenSolJobRecordsRuntimeBlockHeight(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcRuntimeStatsForTest()
	t.Cleanup(data.ResetRpcRuntimeStatsForTest)

	methodCalls := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		methodCalls[req.Method]++

		w.Header().Set("Content-Type", "application/json")
		if req.Method == "getBlockHeight" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  int64(654321),
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": []map[string]interface{}{
				{
					"signature": "solana-test-signature",
					"slot":      uint64(123456),
					"err": map[string]interface{}{
						"InstructionError": []interface{}{0, "Custom"},
					},
					"blockTime": nil,
				},
			},
		})
	}))
	defer server.Close()

	if err := dao.Mdb.Create(&mdb.RpcNode{
		Network: mdb.NetworkSolana,
		Url:     server.URL,
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  1,
		Enabled: true,
		Purpose: mdb.RpcNodePurposeGeneral,
		Status:  mdb.RpcNodeStatusOk,
	}).Error; err != nil {
		t.Fatalf("seed solana rpc_node: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.WalletAddress{
		Network: mdb.NetworkSolana,
		Address: "SolTestAddress001",
		Status:  mdb.TokenStatusEnable,
	}).Error; err != nil {
		t.Fatalf("seed solana wallet: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.ChainToken{
		Network:         mdb.NetworkSolana,
		Symbol:          "SOL",
		ContractAddress: "",
		Decimals:        9,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("seed solana chain_token: %v", err)
	}

	ListenSolJob{}.Run()

	if methodCalls["getSignaturesForAddress"] != 1 {
		t.Fatalf("getSignaturesForAddress calls = %d, want 1", methodCalls["getSignaturesForAddress"])
	}
	if methodCalls["getBlockHeight"] != 1 {
		t.Fatalf("getBlockHeight calls = %d, want 1", methodCalls["getBlockHeight"])
	}

	stats := data.SnapshotRpcRuntimeStats()
	solana := stats[mdb.NetworkSolana]
	if solana.SuccessCount != 2 {
		t.Fatalf("solana success_count = %d, want 2", solana.SuccessCount)
	}
	if solana.FailureCount != 0 {
		t.Fatalf("solana failure_count = %d, want 0", solana.FailureCount)
	}
	if solana.LatestBlockHeight != 654321 {
		t.Fatalf("solana latest_block_height = %d, want 654321", solana.LatestBlockHeight)
	}
	if solana.LastSyncAt.IsZero() {
		t.Fatal("solana last_sync_at is zero")
	}
}

func TestRecordSolanaLatestBlockHeightSkipsWhenPreviousStatsRunning(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcRuntimeStatsForTest()
	t.Cleanup(data.ResetRpcRuntimeStatsForTest)

	gSolanaBlockHeightLock <- struct{}{}
	defer func() { <-gSolanaBlockHeightLock }()

	recordSolanaLatestBlockHeight()

	stats := data.SnapshotRpcRuntimeStats()
	if _, ok := stats[mdb.NetworkSolana]; ok {
		t.Fatalf("solana stats = %#v, want no stats when height reporter is already running", stats[mdb.NetworkSolana])
	}
}
