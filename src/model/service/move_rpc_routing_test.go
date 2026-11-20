package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/dromara/carbon/v2"
)

func seedTestAptosRPCNode(t *testing.T, url, purpose, status string, weight int) mdb.RpcNode {
	t.Helper()
	node := mdb.RpcNode{
		Network: mdb.NetworkAptos,
		Url:     url,
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  weight,
		Enabled: true,
		Purpose: purpose,
		Status:  status,
	}
	if node.Purpose == "" {
		node.Purpose = mdb.RpcNodePurposeGeneral
	}
	if node.Status == "" {
		node.Status = mdb.RpcNodeStatusOk
	}
	if err := dao.Mdb.Create(&node).Error; err != nil {
		t.Fatalf("seed aptos rpc_nodes: %v", err)
	}
	return node
}

func TestAptosGetLedgerVersionUsesRpcNodes(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)

	var disabledCalls int
	disabled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		disabledCalls++
		_, _ = w.Write([]byte(`{"ledger_version":"999"}`))
	}))
	defer disabled.Close()
	disabledNode := mdb.RpcNode{
		Network: mdb.NetworkAptos,
		Url:     disabled.URL,
		Type:    mdb.RpcNodeTypeHttp,
		Weight:  1000,
		Enabled: false,
		Purpose: mdb.RpcNodePurposeGeneral,
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(&disabledNode).Error; err != nil {
		t.Fatalf("seed disabled aptos rpc_nodes: %v", err)
	}
	if err := dao.Mdb.Model(&mdb.RpcNode{}).Where("id = ?", disabledNode.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable aptos rpc_nodes: %v", err)
	}

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ledger_version":"123"}`))
	}))
	defer server.Close()
	seedTestAptosRPCNode(t, server.URL, mdb.RpcNodePurposeGeneral, mdb.RpcNodeStatusOk, 100)

	got, err := AptosGetLedgerVersion()
	if err != nil {
		t.Fatalf("AptosGetLedgerVersion(): %v", err)
	}
	if got != 123 {
		t.Fatalf("ledger version = %d, want 123", got)
	}
	if calls != 1 {
		t.Fatalf("rpc calls = %d, want 1", calls)
	}
	if disabledCalls != 0 {
		t.Fatalf("disabled rpc calls = %d, want 0", disabledCalls)
	}
}

func TestAptosGetTransactionsFallsBackToAlternateRpcNode(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)

	var primaryCalls int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		http.Error(w, "temporary failure", http.StatusBadGateway)
	}))
	defer primary.Close()
	var backupCalls int
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		if r.URL.Path != "/v1/transactions" || r.URL.Query().Get("start") != "10" || r.URL.Query().Get("limit") != "2" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer backup.Close()
	seedTestAptosRPCNode(t, primary.URL, mdb.RpcNodePurposeGeneral, mdb.RpcNodeStatusOk, 100)
	seedTestAptosRPCNode(t, backup.URL, mdb.RpcNodePurposeGeneral, mdb.RpcNodeStatusOk, 1)

	body, err := AptosGetTransactions(10, 2)
	if err != nil {
		t.Fatalf("AptosGetTransactions(): %v", err)
	}
	if string(body) != "[]" {
		t.Fatalf("transactions body = %s, want []", string(body))
	}
	if primaryCalls != 1 || backupCalls != 1 {
		t.Fatalf("rpc calls primary=%d backup=%d, want 1/1", primaryCalls, backupCalls)
	}
}

func TestValidateManualAptosPaymentUsesManualRpcFallback(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.Chain{Network: mdb.NetworkAptos, Enabled: true, MinConfirmations: 1}).Error; err != nil {
		t.Fatalf("create aptos chain: %v", err)
	}
	const usdc = "0xbae207659db88bea0cbead6da0ed00aac12edcdda169e591cd41c94180b46f3b"
	upsertTestChainToken(t, mdb.ChainToken{
		Network:         mdb.NetworkAptos,
		Symbol:          "USDC",
		ContractAddress: usdc,
		Decimals:        6,
		Enabled:         true,
	})

	receive, err := addressutil.NormalizeMoveAddress("0xa")
	if err != nil {
		t.Fatalf("normalize aptos receive: %v", err)
	}
	store, err := addressutil.NormalizeMoveAddress("0x31")
	if err != nil {
		t.Fatalf("normalize aptos store: %v", err)
	}
	senderStore, err := addressutil.NormalizeMoveAddress("0x32")
	if err != nil {
		t.Fatalf("normalize aptos sender store: %v", err)
	}

	var generalCalls int
	general := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generalCalls++
		http.Error(w, "general unavailable", http.StatusBadGateway)
	}))
	defer general.Close()
	var manualCalls int
	manual := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manualCalls++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/transactions/by_hash/"):
			_, _ = w.Write([]byte(`{"type":"user_transaction","success":true,"hash":"0xabc","version":"101","timestamp":"1700000000123456","payload":{"function":"0x1::primary_fungible_store::transfer","arguments":[{"inner":"` + usdc + `"},"` + receive + `","4200000"],"type":"entry_function_payload"},"events":[{"type":"0x1::fungible_asset::Withdraw","data":{"amount":"4200000","store":"` + senderStore + `"}},{"type":"0x1::fungible_asset::Deposit","data":{"amount":"4200000","store":"` + store + `"}}],"changes":[{"address":"` + senderStore + `","data":{"type":"0x1::fungible_asset::FungibleStore","data":{"metadata":{"inner":"` + usdc + `"}}},"type":"write_resource"},{"address":"` + store + `","data":{"type":"0x1::fungible_asset::FungibleStore","data":{"metadata":{"inner":"` + usdc + `"}}},"type":"write_resource"},{"address":"` + store + `","data":{"type":"0x1::object::ObjectCore","data":{"owner":"` + receive + `"}},"type":"write_resource"}]}`))
		case r.URL.Path == "/v1":
			_, _ = w.Write([]byte(`{"ledger_version":"101"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer manual.Close()
	seedTestAptosRPCNode(t, general.URL, mdb.RpcNodePurposeGeneral, mdb.RpcNodeStatusOk, 100)
	seedTestAptosRPCNode(t, manual.URL, mdb.RpcNodePurposeManualVerify, mdb.RpcNodeStatusOk, 1)

	order := &mdb.Orders{
		BaseModel:      mdb.BaseModel{ID: 1, CreatedAt: *carbon.NewTime(carbon.CreateFromTimestampMilli(1699999999000))},
		Network:        mdb.NetworkAptos,
		Token:          "USDC",
		ActualAmount:   4.2,
		ReceiveAddress: receive,
	}
	got, err := ValidateManualAptosPayment(order, "ABC")
	if err != nil {
		t.Fatalf("ValidateManualAptosPayment(): %v", err)
	}
	if got != "0xabc" {
		t.Fatalf("canonical tx id = %q, want %q", got, "0xabc")
	}
	if generalCalls != 1 || manualCalls != 2 {
		t.Fatalf("rpc calls general=%d manual=%d, want 1/2", generalCalls, manualCalls)
	}
}
