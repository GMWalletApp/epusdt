package task

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestTronScannerIdlesWithoutEnabledWallets(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	oldLogger := log.Sugar
	defer func() { log.Sugar = oldLogger }()
	core, logs := observer.New(zapcore.InfoLevel)
	log.Sugar = zap.New(core).Sugar()

	if tronScannerHasWallets() {
		t.Fatal("scanner should idle without wallets")
	}
	idleLogs := logs.FilterMessage("[TRON-BLOCK] no enabled wallet addresses, scanner idle").All()
	if len(idleLogs) != 1 || idleLogs[0].Level != zapcore.WarnLevel {
		t.Fatal("expected WARN idle message")
	}
	wallet := &mdb.WalletAddress{
		Network: mdb.NetworkTron,
		Address: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
		Status:  mdb.TokenStatusEnable,
	}
	if err := dao.Mdb.Create(wallet).Error; err != nil {
		t.Fatalf("seed tron wallet: %v", err)
	}
	if !tronScannerHasWallets() {
		t.Fatal("scanner should resume after adding an enabled wallet")
	}
	if err := dao.Mdb.Model(wallet).Update("status", mdb.TokenStatusDisable).Error; err != nil {
		t.Fatalf("disable tron wallet: %v", err)
	}
	if tronScannerHasWallets() {
		t.Fatal("scanner should idle when wallet is disabled")
	}
}

func TestTronScannerHonorsChainToggle(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"block_header":{"raw_data":{"number":1}}}`))
	}))
	defer server.Close()
	scanner := NewScanner()
	scanner.baseURL = server.URL
	scanner.lastBlock = 1

	for _, enabled := range []bool{false, true, false, true} {
		if err := data.UpdateChainFields(mdb.NetworkTron, map[string]interface{}{"enabled": enabled}); err != nil {
			t.Fatalf("toggle tron chain: %v", err)
		}
		before := requests.Load()
		scanner.poll()
		want := before
		if enabled {
			want++
		}
		if got := requests.Load(); got != want {
			t.Fatalf("enabled=%t: RPC requests = %d, want %d", enabled, got, want)
		}
	}

	if err := dao.Mdb.Where("network = ?", mdb.NetworkTron).Delete(&mdb.Chain{}).Error; err != nil {
		t.Fatalf("delete tron chain: %v", err)
	}
	before := requests.Load()
	scanner.poll()
	if got := requests.Load(); got != before {
		t.Fatalf("missing chain: RPC requests = %d, want %d", got, before)
	}

	done := make(chan struct{})
	go func() {
		scanner.Run()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scanner did not stop when chain was disabled")
	}
}

func TestTronScannerStopsCatchupWhenChainDisabled(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := data.UpdateChainFields(mdb.NetworkTron, map[string]interface{}{"enabled": false}); err != nil {
			t.Errorf("disable tron chain: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"block_header":{"raw_data":{"number":3}}}`))
	}))
	defer server.Close()
	scanner := NewScanner()
	scanner.baseURL = server.URL
	scanner.lastBlock = 1
	scanner.poll()
	if got := requests.Load(); got != 1 {
		t.Fatalf("RPC requests = %d, want only latest block request", got)
	}
	if scanner.lastBlock != 1 {
		t.Fatalf("lastBlock = %d, want 1 when catchup is stopped", scanner.lastBlock)
	}
}

func TestTronScannerSwitchesNodeAfterFailureThreshold(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)

	primary := &mdb.RpcNode{
		Network: mdb.NetworkTron,
		Url:     "https://primary-tron.example.com",
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  100,
		Enabled: true,
		Purpose: mdb.RpcNodePurposeGeneral,
		Status:  mdb.RpcNodeStatusOk,
	}
	backup := &mdb.RpcNode{
		Network: mdb.NetworkTron,
		Url:     "https://backup-tron.example.com",
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  1,
		Enabled: true,
		Purpose: mdb.RpcNodePurposeGeneral,
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(primary).Error; err != nil {
		t.Fatalf("seed primary rpc_node: %v", err)
	}
	if err := dao.Mdb.Create(backup).Error; err != nil {
		t.Fatalf("seed backup rpc_node: %v", err)
	}

	scanner := NewScanner()
	scanner.useRpcNode(primary)
	for i := 0; i < data.RpcFailoverThreshold-1; i++ {
		scanner.recordRpcFailure("test")
		if scanner.nodeID != primary.ID {
			t.Fatalf("node switched before threshold to id=%d", scanner.nodeID)
		}
	}

	scanner.recordRpcFailure("test")
	if scanner.nodeID != backup.ID {
		t.Fatalf("scanner node id = %d, want backup id=%d", scanner.nodeID, backup.ID)
	}
	if scanner.baseURL != backup.Url {
		t.Fatalf("scanner baseURL = %q, want %q", scanner.baseURL, backup.Url)
	}
}

func TestTronScannerStopsOnHistoricalBlockFetchError(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)

	node := &mdb.RpcNode{
		Network: mdb.NetworkTron,
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  1,
		Enabled: true,
		Purpose: mdb.RpcNodePurposeGeneral,
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(node).Error; err != nil {
		t.Fatalf("seed rpc_node: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/getnowblock":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"block_header": map[string]interface{}{
					"raw_data": map[string]interface{}{
						"number":    3,
						"timestamp": int64(1000),
					},
				},
				"transactions": []interface{}{},
			})
		case "/wallet/getblockbynum":
			http.Error(w, "temporary", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	node.Url = server.URL
	scanner := NewScanner()
	scanner.useRpcNode(node)
	scanner.lastBlock = 1

	scanner.poll()
	if scanner.lastBlock != 1 {
		t.Fatalf("lastBlock = %d, want 1 so failed block is retried", scanner.lastBlock)
	}
}

func TestTronRPCRecordsRuntimeStats(t *testing.T) {
	data.ResetRpcRuntimeStatsForTest()
	t.Cleanup(data.ResetRpcRuntimeStatsForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/getnowblock":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"block_header": map[string]interface{}{
					"raw_data": map[string]interface{}{
						"number":    10,
						"timestamp": int64(1000),
					},
				},
				"transactions": []interface{}{},
			})
		case "/wallet/getblockbynum":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"block_header": map[string]interface{}{
					"raw_data": map[string]interface{}{
						"number":    9,
						"timestamp": int64(1000),
					},
				},
				"transactions": []interface{}{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if _, err := GetNowBlock(server.URL, ""); err != nil {
		t.Fatalf("GetNowBlock(): %v", err)
	}
	if _, err := GetBlockByNum(server.URL, "", 9); err != nil {
		t.Fatalf("GetBlockByNum(): %v", err)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary", http.StatusBadGateway)
	}))
	defer failing.Close()
	if _, err := GetBlockByNum(failing.URL, "", 8); err == nil {
		t.Fatal("GetBlockByNum() error = nil, want failure")
	}

	stats := data.SnapshotRpcRuntimeStats()
	tron := stats[mdb.NetworkTron]
	if tron.SuccessCount != 2 {
		t.Fatalf("tron success_count = %d, want 2", tron.SuccessCount)
	}
	if tron.FailureCount != 1 {
		t.Fatalf("tron failure_count = %d, want 1", tron.FailureCount)
	}
	if tron.LatestBlockHeight != 10 {
		t.Fatalf("tron latest_block_height = %d, want 10", tron.LatestBlockHeight)
	}
	if tron.LastSyncAt.IsZero() {
		t.Fatal("tron last_sync_at is zero")
	}
}
