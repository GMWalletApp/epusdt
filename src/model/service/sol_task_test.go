package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/tidwall/gjson"
)

func setupSolanaRPCNode(t *testing.T, url string) func() {
	t.Helper()

	cleanup := testutil.SetupTestDatabases(t)
	node := &mdb.RpcNode{
		Network: mdb.NetworkSolana,
		Url:     url,
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  1,
		Enabled: true,
		Purpose: mdb.RpcNodePurposeGeneral,
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(node).Error; err != nil {
		cleanup()
		t.Fatalf("seed solana rpc_node: %v", err)
	}
	return cleanup
}

func TestResolveSolanaRpcURLRequiresRpcNode(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if got, err := resolveSolanaRpcURL(); err == nil {
		t.Fatalf("resolveSolanaRpcURL() = %q, nil; want error", got)
	}
}

func TestResolveSolanaRpcURLWithRow(t *testing.T) {
	cleanup := setupSolanaRPCNode(t, " https://solana.example.com ")
	defer cleanup()

	got, err := resolveSolanaRpcURL()
	if err != nil {
		t.Fatalf("resolveSolanaRpcURL(): %v", err)
	}
	if got != "https://solana.example.com" {
		t.Fatalf("resolveSolanaRpcURL() = %q, want https://solana.example.com", got)
	}
}

func TestResolveSolanaRpcURLIgnoresManualVerifyOnly(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.RpcNode{
		Network: mdb.NetworkSolana,
		Url:     "https://paid-solana.example.com",
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  100,
		Enabled: true,
		Purpose: mdb.RpcNodePurposeManualVerify,
		Status:  mdb.RpcNodeStatusOk,
	}).Error; err != nil {
		t.Fatalf("seed manual rpc_node: %v", err)
	}

	if got, err := resolveSolanaRpcURL(); err == nil {
		t.Fatalf("resolveSolanaRpcURL() = %q, nil; want error", got)
	}
}

func TestResolveSolanaRpcURLUsesGeneralWhenManualVerifyExists(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	rows := []mdb.RpcNode{
		{Network: mdb.NetworkSolana, Url: "https://paid-solana.example.com", Type: mdb.RpcNodeTypeHttp, Weight: 100, Enabled: true, Purpose: mdb.RpcNodePurposeManualVerify, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkSolana, Url: " https://general-solana.example.com ", Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
	}
	if err := dao.Mdb.Create(&rows).Error; err != nil {
		t.Fatalf("seed rpc_nodes: %v", err)
	}

	got, err := resolveSolanaRpcURL()
	if err != nil {
		t.Fatalf("resolveSolanaRpcURL(): %v", err)
	}
	if got != "https://general-solana.example.com" {
		t.Fatalf("resolveSolanaRpcURL() = %q, want general node", got)
	}
}

func TestSolGetBlockHeightDoesNotAffectRpcNodeFailover(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	data.ResetRpcRuntimeStatsForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)
	t.Cleanup(data.ResetRpcRuntimeStatsForTest)

	oldRetryCount := solRPCRetryCount
	solRPCRetryCount = 0
	t.Cleanup(func() {
		solRPCRetryCount = oldRetryCount
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary", http.StatusBadGateway)
	}))
	defer server.Close()

	node := &mdb.RpcNode{
		Network: mdb.NetworkSolana,
		Url:     server.URL,
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  1,
		Enabled: true,
		Purpose: mdb.RpcNodePurposeGeneral,
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(node).Error; err != nil {
		t.Fatalf("seed solana rpc_node: %v", err)
	}

	for i := 0; i < data.RpcFailoverThreshold+1; i++ {
		if _, err := SolGetBlockHeight(); err == nil {
			t.Fatalf("SolGetBlockHeight attempt %d unexpectedly succeeded", i+1)
		}
	}

	if data.IsRpcNodeCoolingDown(node.ID) {
		t.Fatal("SolGetBlockHeight failures should not affect rpc_node failover cooldown")
	}
	stats := data.SnapshotRpcRuntimeStats()
	if got := stats[mdb.NetworkSolana].FailureCount; got != int64(data.RpcFailoverThreshold+1) {
		t.Fatalf("solana runtime failure_count = %d, want %d", got, data.RpcFailoverThreshold+1)
	}
}

func TestSolRetryClientSwitchesAfterFailureThreshold(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)
	oldRetryCount := solRPCRetryCount
	oldRetryWait := solRPCRetryWait
	oldRetryMaxWait := solRPCRetryMaxWait
	solRPCRetryCount = 0
	solRPCRetryWait = time.Millisecond
	solRPCRetryMaxWait = time.Millisecond
	t.Cleanup(func() {
		solRPCRetryCount = oldRetryCount
		solRPCRetryWait = oldRetryWait
		solRPCRetryMaxWait = oldRetryMaxWait
	})

	var primaryCalls int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		http.Error(w, "temporary", http.StatusBadGateway)
	}))
	defer primary.Close()

	var backupCalls int
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "ok",
		})
	}))
	defer backup.Close()

	rows := []mdb.RpcNode{
		{Network: mdb.NetworkSolana, Url: primary.URL, Type: mdb.RpcNodeTypeHttp, Weight: 100, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkSolana, Url: backup.URL, Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
	}
	if err := dao.Mdb.Create(&rows).Error; err != nil {
		t.Fatalf("seed rpc_nodes: %v", err)
	}

	for i := 0; i < data.RpcFailoverThreshold-1; i++ {
		if _, err := SolRetryClient("getHealth", nil); err == nil {
			t.Fatalf("SolRetryClient attempt %d unexpectedly succeeded", i+1)
		}
	}
	if backupCalls != 0 {
		t.Fatalf("backup calls before threshold = %d, want 0", backupCalls)
	}

	body, err := SolRetryClient("getHealth", nil)
	if err != nil {
		t.Fatalf("SolRetryClient after threshold: %v", err)
	}
	if gjson.GetBytes(body, "result").String() != "ok" {
		t.Fatalf("response body = %s, want result ok", string(body))
	}
	if backupCalls == 0 {
		t.Fatal("backup RPC was not called after threshold")
	}
	if primaryCalls == 0 {
		t.Fatal("primary RPC was not called")
	}
}

func TestSolRetryClientCountsJSONRPCErrorAsNodeFailure(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)
	oldRetryCount := solRPCRetryCount
	oldRetryWait := solRPCRetryWait
	oldRetryMaxWait := solRPCRetryMaxWait
	solRPCRetryCount = 0
	solRPCRetryWait = time.Millisecond
	solRPCRetryMaxWait = time.Millisecond
	t.Cleanup(func() {
		solRPCRetryCount = oldRetryCount
		solRPCRetryWait = oldRetryWait
		solRPCRetryMaxWait = oldRetryMaxWait
	})

	var primaryCalls int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]interface{}{
				"code": -32005,
			},
		})
	}))
	defer primary.Close()

	var backupCalls int
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "ok",
		})
	}))
	defer backup.Close()

	rows := []mdb.RpcNode{
		{Network: mdb.NetworkSolana, Url: primary.URL, Type: mdb.RpcNodeTypeHttp, Weight: 100, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkSolana, Url: backup.URL, Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
	}
	if err := dao.Mdb.Create(&rows).Error; err != nil {
		t.Fatalf("seed rpc_nodes: %v", err)
	}

	for i := 0; i < data.RpcFailoverThreshold-1; i++ {
		if _, err := SolRetryClient("getHealth", nil); err == nil {
			t.Fatalf("SolRetryClient attempt %d unexpectedly succeeded", i+1)
		}
	}
	body, err := SolRetryClient("getHealth", nil)
	if err != nil {
		t.Fatalf("SolRetryClient after JSON-RPC error threshold: %v", err)
	}
	if gjson.GetBytes(body, "result").String() != "ok" {
		t.Fatalf("response body = %s, want result ok", string(body))
	}
	if primaryCalls < data.RpcFailoverThreshold {
		t.Fatalf("primary calls = %d, want at least threshold", primaryCalls)
	}
	if backupCalls == 0 {
		t.Fatal("backup RPC was not called after JSON-RPC error threshold")
	}
}

func TestSolRetryClientWithURLHeadersForwardsCustomHeaders(t *testing.T) {
	oldRetryCount := solRPCRetryCount
	oldRetryWait := solRPCRetryWait
	oldRetryMaxWait := solRPCRetryMaxWait
	solRPCRetryCount = 0
	solRPCRetryWait = time.Millisecond
	solRPCRetryMaxWait = time.Millisecond
	t.Cleanup(func() {
		solRPCRetryCount = oldRetryCount
		solRPCRetryWait = oldRetryWait
		solRPCRetryMaxWait = oldRetryMaxWait
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Test-Header"); got != "test-value" {
			t.Errorf("X-Test-Header = %q, want test-value", got)
			http.Error(w, "bad custom header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "ok",
		})
	}))
	defer server.Close()

	headers := http.Header{}
	headers.Set("X-Test-Header", "test-value")
	body, err := solRetryClientWithURLHeaders(server.URL, "getHealth", nil, headers)
	if err != nil {
		t.Fatalf("solRetryClientWithURLHeaders(): %v", err)
	}
	if gjson.GetBytes(body, "result").String() != "ok" {
		t.Fatalf("response body = %s, want result ok", string(body))
	}
}

func TestSolRetryClientWithURLDoesNotForwardUserIPByDefault(t *testing.T) {
	oldRetryCount := solRPCRetryCount
	oldRetryWait := solRPCRetryWait
	oldRetryMaxWait := solRPCRetryMaxWait
	solRPCRetryCount = 0
	solRPCRetryWait = time.Millisecond
	solRPCRetryMaxWait = time.Millisecond
	t.Cleanup(func() {
		solRPCRetryCount = oldRetryCount
		solRPCRetryWait = oldRetryWait
		solRPCRetryMaxWait = oldRetryMaxWait
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-USER-IP"); got != "" {
			t.Errorf("X-USER-IP header = %q, want empty", got)
			http.Error(w, "unexpected user ip header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "ok",
		})
	}))
	defer server.Close()

	body, err := solRetryClientWithURL(server.URL, "getHealth", nil)
	if err != nil {
		t.Fatalf("solRetryClientWithURL(): %v", err)
	}
	if gjson.GetBytes(body, "result").String() != "ok" {
		t.Fatalf("response body = %s, want result ok", string(body))
	}
}

func TestSolCallBackDoesNotCacheSignatureAfterRetryableOrderProcessingError(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	clearProcessedSignatures()
	t.Cleanup(clearProcessedSignatures)

	const (
		address = "2uFTf9TZ8gd7Kg6hkb79TxfaeNpaAgpJ8uVHguv2Yweu"
		sig     = "retryable-sol-signature"
		tradeID = "sol-retryable-order-001"
		amount  = 1.23
	)
	blockTime := time.Now().Add(time.Minute).Unix()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpcReq struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&rpcReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "getSignaturesForAddress":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"result": []map[string]interface{}{
					{
						"signature": sig,
						"slot":      1,
						"err":       nil,
						"blockTime": blockTime,
					},
				},
			})
		case "getTransaction":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]interface{}{
					"blockTime": blockTime,
					"meta":      map[string]interface{}{"err": nil},
					"transaction": map[string]interface{}{
						"message": map[string]interface{}{
							"instructions": []map[string]interface{}{
								{
									"programId": SystemProgramID,
									"parsed": map[string]interface{}{
										"type": "transfer",
										"info": map[string]interface{}{
											"source":      "source-wallet",
											"destination": address,
											"lamports":    1_230_000_000,
										},
									},
								},
							},
						},
					},
				},
			})
		default:
			http.Error(w, "unexpected method "+rpcReq.Method, http.StatusBadRequest)
		}
	}))
	defer server.Close()

	if err := dao.Mdb.Create(&mdb.RpcNode{
		Network: mdb.NetworkSolana,
		Url:     server.URL,
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  1,
		Enabled: true,
		Status:  mdb.RpcNodeStatusOk,
	}).Error; err != nil {
		t.Fatalf("seed solana rpc_node: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.ChainToken{
		Network:         mdb.NetworkSolana,
		Symbol:          "SOL",
		ContractAddress: "",
		Decimals:        SOL_Decimals,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("seed SOL chain token: %v", err)
	}
	order := &mdb.Orders{
		TradeId:        tradeID,
		OrderId:        "merchant-sol-retryable-001",
		Amount:         100,
		Currency:       "CNY",
		ActualAmount:   amount,
		ReceiveAddress: address,
		Token:          "SOL",
		Network:        mdb.NetworkSolana,
		Status:         mdb.StatusWaitPay,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := data.LockTransaction(mdb.NetworkSolana, address, "SOL", tradeID, amount, time.Hour); err != nil {
		t.Fatalf("lock transaction: %v", err)
	}

	oldProcessSolanaOrder := processSolanaOrder
	calls := 0
	processSolanaOrder = func(req *request.OrderProcessingRequest) error {
		calls++
		if req.TradeId != tradeID {
			t.Fatalf("order processing trade_id = %q, want %q", req.TradeId, tradeID)
		}
		return errors.New("temporary database error")
	}
	t.Cleanup(func() {
		processSolanaOrder = oldProcessSolanaOrder
	})

	var wg sync.WaitGroup
	wg.Add(1)
	SolCallBack(address, &wg)
	wg.Wait()

	if calls != 1 {
		t.Fatalf("order processing calls = %d, want 1", calls)
	}
	if _, ok := gProcessedSignatures.Load(sig); ok {
		t.Fatalf("signature %s was cached after retryable order processing error", sig)
	}
}

func TestSolClientHealthy(t *testing.T) {
	requireSolanaIntegration(t)

	bodyData, err := SolRetryClient("getHealth", nil)
	if err != nil {
		t.Fatalf("SolRetryClient failed: %v", err)
	}

	var result map[string]interface{}
	err = json.Unmarshal(bodyData, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	status, ok := result["result"].(string)
	if !ok {
		t.Fatalf("Unexpected response format: %v", result)
	}

	t.Logf("RPC Health Status: %s", status)

	if status != "ok" {
		t.Errorf("Expected health status 'ok', got '%s'", status)
	}
}

func clearProcessedSignatures() {
	gProcessedSignatures.Range(func(key, value interface{}) bool {
		gProcessedSignatures.Delete(key)
		return true
	})
}

func TestSolClientGetSignaturesForAddress(t *testing.T) {
	requireSolanaIntegration(t)

	// Example wallet address (replace with actual test address)
	address := "2uFTf9TZ8gd7Kg6hkb79TxfaeNpaAgpJ8uVHguv2Yweu"

	bodyData, err := SolRetryClient("getSignaturesForAddress", []interface{}{address, map[string]interface{}{"commitment": "finalized", "limit": 100}})
	if err != nil {
		t.Fatalf("SolRetryClient failed: %v", err)
	}

	var result map[string]interface{}
	err = json.Unmarshal(bodyData, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	signatures, ok := result["result"].([]interface{})
	if !ok {
		t.Fatalf("Unexpected response format: %v", result)
	}

	t.Logf("Found %d signatures for address %s", len(signatures), address)

}

func TestSolClientGetTransaction(t *testing.T) {
	requireSolanaIntegration(t)

	// Example transaction signature (replace with actual test signature)
	sig := "2aEoNykk4ZJ27C3y7EDJiQUc7GFnnsMe7ofFzB73swGL8kTxSBFCnwzWw3jzr3BND7k8hx15fZHUUAbG1XemNFe5"

	txData, err := SolRetryClient("getTransaction", []interface{}{sig, map[string]interface{}{"encoding": "jsonParsed", "commitment": "finalized"}})
	if err != nil {
		t.Fatalf("SolRetryClient failed: %v", err)
	}

	var result map[string]interface{}
	err = json.Unmarshal(txData, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	txInfo, ok := result["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("Unexpected response format: %v", result)
	}

	t.Logf("Transaction Info for signature %s: %v", sig, txInfo)
}

func requireSolanaIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() || os.Getenv("RUN_SOLANA_INTEGRATION_TESTS") == "" {
		t.Skip("set RUN_SOLANA_INTEGRATION_TESTS=1 to run Solana public RPC integration tests")
	}
	cleanup := setupSolanaRPCNode(t, "https://api.mainnet-beta.solana.com")
	t.Cleanup(cleanup)
}

func TestFindATAAddress(t *testing.T) {
	tests := []struct {
		name  string
		owner string
		mint  string
		want  string
	}{
		{
			name:  "RAY token ATA",
			owner: "2uFTf9TZ8gd7Kg6hkb79TxfaeNpaAgpJ8uVHguv2Yweu",
			mint:  "4k3Dyjzvzp8eMZWUXbBCjEvwSkkk59S5iCNLY3QrkX6R",
			want:  "GgmJrwuP946uV8qAwsnXxzYrJqEwW6eGnsVnQZFS5rp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ata, err := FindATAAddress(tt.owner, tt.mint)
			if err != nil {
				t.Fatalf("FindATAAddress failed: %v", err)
			}

			t.Logf("Owner: %s", tt.owner)
			t.Logf("Mint: %s", tt.mint)
			t.Logf("ATA: %s", ata)

			if tt.want != "" && ata != tt.want {
				t.Errorf("Expected ATA %s, got %s", tt.want, ata)
			}
		})
	}
}

func TestMatchATAAddress(t *testing.T) {
	owner := "2uFTf9TZ8gd7Kg6hkb79TxfaeNpaAgpJ8uVHguv2Yweu"
	mint := "4k3Dyjzvzp8eMZWUXbBCjEvwSkkk59S5iCNLY3QrkX6R" // ray token
	expectedATA := "GgmJrwuP946uV8qAwsnXxzYrJqEwW6eGnsVnQZFS5rp4"

	ok := MatchAtaAddress(owner, mint, expectedATA)
	t.Logf("Owner: %s", owner)
	t.Logf("Mint: %s", mint)
	t.Logf("Expected ATA: %s", expectedATA)
	t.Logf("Match result: %v", ok)

	if !ok {
		t.Error("Expected ATA to match, but it didn't")
	}
}

func TestMatchUsdtAtaAddress(t *testing.T) {
	// Example wallet address (replace with actual test address)
	owner := "2uFTf9TZ8gd7Kg6hkb79TxfaeNpaAgpJ8uVHguv2Yweu"

	ata, err := FindATAAddress(owner, USDT_Mint)
	if err != nil {
		t.Fatalf("FindATAAddress failed: %v", err)
	}

	t.Logf("Owner: %s", owner)
	t.Logf("USDT Mint: %s", USDT_Mint)
	t.Logf("USDT ATA: %s", ata)

	ok := MatchUsdtAtaAddress(owner, ata)
	if !ok {
		t.Error("Expected USDT ATA to match")
	}
}

func TestMatchUsdcAtaAddress(t *testing.T) {
	// Example wallet address (replace with actual test address)
	owner := "2uFTf9TZ8gd7Kg6hkb79TxfaeNpaAgpJ8uVHguv2Yweu"

	ata, err := FindATAAddress(owner, USDC_Mint)
	if err != nil {
		t.Fatalf("FindATAAddress failed: %v", err)
	}

	t.Logf("Owner: %s", owner)
	t.Logf("USDC Mint: %s", USDC_Mint)
	t.Logf("USDC ATA: %s", ata)

	ok := MatchUsdcAtaAddress(owner, ata)
	if !ok {
		t.Error("Expected USDC ATA to match")
	}
}

func TestAdjustAmount(t *testing.T) {
	tests := []struct {
		name     string
		amount   uint64
		decimals int
		want     float64
	}{
		{
			name:     "USDT amount (6 decimals)",
			amount:   123456789,
			decimals: 6,
			want:     123.456789,
		},
		{
			name:     "USDC amount (6 decimals)",
			amount:   1000000,
			decimals: 6,
			want:     1.0,
		},
		{
			name:     "SOL amount (9 decimals)",
			amount:   1000000000,
			decimals: 9,
			want:     1.0,
		},
		{
			name:     "Zero amount",
			amount:   0,
			decimals: 6,
			want:     0,
		},
		{
			name:     "Small amount",
			amount:   1,
			decimals: 6,
			want:     0.000001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adjusted := ADJustAmount(tt.amount, tt.decimals)
			t.Logf("Raw amount: %d, Decimals: %d, Adjusted: %.2f", tt.amount, tt.decimals, adjusted)

			if adjusted != tt.want {
				t.Errorf("Expected %.2f, got %.2f", tt.want, adjusted)
			}
		})
	}
}

func TestParseTransferInfoFromInstruction_SplTransfer(t *testing.T) {
	requireSolanaIntegration(t)

	// SPL Token "transfer" (no mint in instruction, must look up from postTokenBalances)
	sig := "3tZTwLrvmiZ59h4UzyMHPd7DPux7t9eXZgkUvEfquaoSuERrPSRNzWuSHKQM2fbiCWFDGNqoLpu2kLZnfoegVpqN"
	txData, err := SolGetTransaction(sig)
	if err != nil {
		t.Fatalf("SolGetTransaction failed: %v", err)
	}

	instructions := gjson.GetBytes(txData, "result.transaction.message.instructions").Array()
	var found bool
	for _, inst := range instructions {
		info, err := ParseTransferInfoFromInstruction(inst, txData)
		if err != nil {
			t.Logf("parse error (ok to skip): %v", err)
			continue
		}
		if info == nil {
			continue
		}
		found = true
		t.Logf("SPL transfer: source=%s dest=%s mint=%s amount=%.6f raw=%d blockTime=%d",
			info.Source, info.Destination, info.Mint, info.Amount, info.RawAmount, info.BlockTime)

		if info.Mint == "" {
			t.Error("Expected mint to be resolved from postTokenBalances")
		}
		if info.RawAmount != 50000 {
			t.Errorf("Expected raw amount 50000, got %d", info.RawAmount)
		}
		if info.BlockTime == 0 {
			t.Error("Expected non-zero blockTime")
		}
	}
	if !found {
		t.Error("No transfer instruction found in transaction")
	}
}

func TestParseTransferInfoFromInstruction_TransferChecked(t *testing.T) {
	requireSolanaIntegration(t)

	// SPL Token "transferChecked" (has mint and tokenAmount in instruction)
	sig := "2aEoNykk4ZJ27C3y7EDJiQUc7GFnnsMe7ofFzB73swGL8kTxSBFCnwzWw3jzr3BND7k8hx15fZHUUAbG1XemNFe5"
	txData, err := SolGetTransaction(sig)
	if err != nil {
		t.Fatalf("SolGetTransaction failed: %v", err)
	}

	instructions := gjson.GetBytes(txData, "result.transaction.message.instructions").Array()
	var found bool
	for _, inst := range instructions {
		info, err := ParseTransferInfoFromInstruction(inst, txData)
		if err != nil {
			t.Logf("parse error (ok to skip): %v", err)
			continue
		}
		if info == nil {
			continue
		}
		found = true
		t.Logf("TransferChecked: source=%s dest=%s mint=%s amount=%.6f raw=%d blockTime=%d",
			info.Source, info.Destination, info.Mint, info.Amount, info.RawAmount, info.BlockTime)

		if info.Mint != USDT_Mint {
			t.Errorf("Expected USDT mint %s, got %s", USDT_Mint, info.Mint)
		}
		if info.RawAmount != 300000 {
			t.Errorf("Expected raw amount 300000, got %d", info.RawAmount)
		}
		if info.Amount != 0.3 {
			t.Errorf("Expected amount 0.3, got %f", info.Amount)
		}
		if info.BlockTime == 0 {
			t.Error("Expected non-zero blockTime")
		}
	}
	if !found {
		t.Error("No transferChecked instruction found in transaction")
	}
}

func TestParseTransferInfoFromInstruction_SystemTransfer(t *testing.T) {
	requireSolanaIntegration(t)

	// System program SOL transfer
	sig := "5pNMonUBvLVpxXTmyd5CGVBs49W6781g2ACnrCXhbmtz58KENYA7HSqu6hQkQweg3qQboRd8WAscphNAtiq9UtZZ"
	txData, err := SolGetTransaction(sig)
	if err != nil {
		t.Fatalf("SolGetTransaction failed: %v", err)
	}

	instructions := gjson.GetBytes(txData, "result.transaction.message.instructions").Array()
	transferCount := 0
	for _, inst := range instructions {
		info, err := ParseTransferInfoFromInstruction(inst, txData)
		if err != nil {
			t.Logf("parse error (ok to skip): %v", err)
			continue
		}
		if info == nil {
			continue
		}
		transferCount++
		t.Logf("System transfer #%d: source=%s dest=%s mint=%s amount=%.9f raw=%d blockTime=%d",
			transferCount, info.Source, info.Destination, info.Mint, info.Amount, info.RawAmount, info.BlockTime)

		if info.Mint != "SOL" {
			t.Errorf("Expected mint SOL, got %s", info.Mint)
		}
		if info.RawAmount == 0 {
			t.Error("Expected non-zero raw amount")
		}
		if info.BlockTime == 0 {
			t.Error("Expected non-zero blockTime")
		}
	}
	if transferCount == 0 {
		t.Error("No system transfer instruction found")
	}
	t.Logf("Found %d system transfers", transferCount)
}
