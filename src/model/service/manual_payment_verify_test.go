package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/dromara/carbon/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/jetton"
	"github.com/xssnick/tonutils-go/tvm/cell"
	"gorm.io/gorm/clause"
)

func upsertTestChainToken(t *testing.T, token mdb.ChainToken) {
	t.Helper()
	if err := dao.Mdb.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "network"}, {Name: "symbol"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"contract_address",
			"decimals",
			"enabled",
			"min_amount",
		}),
	}).Create(&token).Error; err != nil {
		t.Fatalf("upsert token: %v", err)
	}
}

func TestManualVerifyEvmHashAcceptsOptional0x(t *testing.T) {
	hash := strings.Repeat("a", 64)
	if !isEvmHash(hash) {
		t.Fatal("expected bare EVM hash to be valid")
	}
	if !isEvmHash("0x" + hash) {
		t.Fatal("expected 0x-prefixed EVM hash to be valid")
	}
	if isEvmHash("0x" + strings.Repeat("a", 63)) {
		t.Fatal("expected short EVM hash to be invalid")
	}
}

func TestManualVerifyNormalizeEvmAddressAcceptsOptional0x(t *testing.T) {
	addr := "1111111111111111111111111111111111111111"
	want := common.HexToAddress("0x" + addr)
	got, err := normalizeEvmAddress(addr)
	if err != nil {
		t.Fatalf("normalize bare address: %v", err)
	}
	if got != want {
		t.Fatalf("bare address = %s, want %s", got.Hex(), want.Hex())
	}
	got, err = normalizeEvmAddress("0X" + strings.ToUpper(addr))
	if err != nil {
		t.Fatalf("normalize prefixed address: %v", err)
	}
	if got != want {
		t.Fatalf("prefixed address = %s, want %s", got.Hex(), want.Hex())
	}
}

func TestManualVerifyNormalizeTronAddressHexAcceptsOptionalPrefix(t *testing.T) {
	body := "a614f803b6fd780986a42c78ec9c7f77e6ded13c"
	want := "41" + body

	for _, input := range []string{body, "0x" + body, want, "0X" + strings.ToUpper(want)} {
		got, err := normalizeTronAddressHex(input)
		if err != nil {
			t.Fatalf("normalizeTronAddressHex(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeTronAddressHex(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestManualVerifyNormalizeTronTxIDAcceptsOptional0x(t *testing.T) {
	txID := strings.Repeat("a", 64)
	got, err := normalizeTronTxID(txID)
	if err != nil {
		t.Fatalf("normalize bare txid: %v", err)
	}
	if got != txID {
		t.Fatalf("bare txid = %q, want %q", got, txID)
	}
	got, err = normalizeTronTxID("0X" + strings.ToUpper(txID))
	if err != nil {
		t.Fatalf("normalize prefixed txid: %v", err)
	}
	if got != txID {
		t.Fatalf("prefixed txid = %q, want %q", got, txID)
	}
	if _, err = normalizeTronTxID("0x" + strings.Repeat("a", 63)); err == nil {
		t.Fatal("expected short txid to fail")
	}
}

func TestValidateManualAptosPayment(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.Chain{Network: mdb.NetworkAptos, Enabled: true, MinConfirmations: 1}).Error; err != nil {
		t.Fatalf("create aptos chain: %v", err)
	}
	usdc := "0xbae207659db88bea0cbead6da0ed00aac12edcdda169e591cd41c94180b46f3b"
	if err := dao.Mdb.Create(&mdb.ChainToken{
		Network:         mdb.NetworkAptos,
		Symbol:          "USDC",
		ContractAddress: usdc,
		Decimals:        6,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("create aptos usdc token: %v", err)
	}

	receive, err := addressutil.NormalizeMoveAddress("0xa")
	if err != nil {
		t.Fatalf("normalize aptos receive: %v", err)
	}
	store, err := addressutil.NormalizeMoveAddress("0x11")
	if err != nil {
		t.Fatalf("normalize aptos store: %v", err)
	}
	senderStore, err := addressutil.NormalizeMoveAddress("0x12")
	if err != nil {
		t.Fatalf("normalize aptos sender store: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer server.Close()
	seedTestAptosRPCNode(t, server.URL, mdb.RpcNodePurposeGeneral, mdb.RpcNodeStatusOk, 100)

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
}

func TestParseManualTonTxRefAcceptsCanonicalLTAndHashOnly(t *testing.T) {
	receiveRaw := "0:ba295e33b3c4c9b5265aa4ead1166a92931ce9abea120a8c5e91044a1257f89c"
	hash := strings.Repeat("a", 64)

	ref, err := parseManualTonTxRef("ton:" + receiveRaw + ":123:" + strings.ToUpper(hash))
	if err != nil {
		t.Fatalf("parse canonical TON tx ref: %v", err)
	}
	if ref.ReceiveRaw != receiveRaw || ref.LT != 123 || ref.HashHex != hash || !ref.HasLT {
		t.Fatalf("canonical ref = %#v", ref)
	}

	ref, err = parseManualTonTxRef("456:0X" + strings.ToUpper(hash))
	if err != nil {
		t.Fatalf("parse lt:hash TON tx ref: %v", err)
	}
	if ref.ReceiveRaw != "" || ref.LT != 456 || ref.HashHex != hash || !ref.HasLT {
		t.Fatalf("lt:hash ref = %#v", ref)
	}

	ref, err = parseManualTonTxRef("0X" + strings.ToUpper(hash))
	if err != nil {
		t.Fatalf("parse hash-only TON tx ref: %v", err)
	}
	if ref.ReceiveRaw != "" || ref.LT != 0 || ref.HashHex != hash || ref.HasLT {
		t.Fatalf("hash-only ref = %#v", ref)
	}
}

type fakeManualTonAPI struct {
	ton.APIClientWrapped
	master          *ton.BlockIDExt
	account         *tlb.Account
	txs             []*tlb.Transaction
	getAccountCalls int
	listCalls       []manualTonListCall
}

type manualTonListCall struct {
	limit   uint32
	lt      uint64
	hashHex string
}

func (f *fakeManualTonAPI) CurrentMasterchainInfo(context.Context) (*ton.BlockIDExt, error) {
	return f.master, nil
}

func (f *fakeManualTonAPI) WaitForBlock(uint32) ton.APIClientWrapped {
	return f
}

func (f *fakeManualTonAPI) GetAccount(context.Context, *ton.BlockIDExt, *address.Address) (*tlb.Account, error) {
	f.getAccountCalls++
	if f.account == nil {
		return &tlb.Account{}, nil
	}
	return f.account, nil
}

func (f *fakeManualTonAPI) ListTransactions(_ context.Context, _ *address.Address, limit uint32, lt uint64, txHash []byte) ([]*tlb.Transaction, error) {
	f.listCalls = append(f.listCalls, manualTonListCall{
		limit:   limit,
		lt:      lt,
		hashHex: hex.EncodeToString(txHash),
	})
	return f.txs, nil
}

func TestValidateManualTonPaymentWithAPINativeTON(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ? AND symbol = ?", mdb.NetworkTon, "USDT").
		Update("enabled", false).Error; err != nil {
		t.Fatalf("disable TON USDT token: %v", err)
	}

	receive := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	sender := address.NewAddress(0, 0, bytes.Repeat([]byte{0x66}, 32)).Bounce(false).Testnet(false)
	tx := tonTestInboundTx(t, sender, receive, tlb.MustFromTON("1.23"), nil)
	order := &mdb.Orders{
		BaseModel:      mdb.BaseModel{ID: 1, CreatedAt: *carbon.NewTime(carbon.CreateFromTimestampMilli(time.Now().Add(-time.Minute).UnixMilli()))},
		Network:        mdb.NetworkTon,
		Token:          "TON",
		ActualAmount:   1.23,
		ReceiveAddress: receive.Bounce(false).String(),
	}
	api := &fakeManualTonAPI{
		master: &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: -0x8000000000000000, SeqNo: 9},
		txs:    []*tlb.Transaction{tx},
	}
	ref := manualTonTxRef{LT: tx.LT, HashHex: hex.EncodeToString(tx.Hash), HasLT: true}

	got, err := validateManualTonPaymentWithAPI(context.Background(), api, order, receive, ref)
	if err != nil {
		t.Fatalf("validateManualTonPaymentWithAPI(): %v", err)
	}
	if api.getAccountCalls != 0 {
		t.Fatalf("exact TON ref fetched account state %d times, want 0", api.getAccountCalls)
	}
	if len(api.listCalls) != 1 {
		t.Fatalf("ListTransactions calls = %#v, want one exact lookup", api.listCalls)
	}
	if call := api.listCalls[0]; call.limit != 1 || call.lt != tx.LT || call.hashHex != hex.EncodeToString(tx.Hash) {
		t.Fatalf("ListTransactions exact call = %#v, want limit=1 lt/hash from ref", call)
	}
	want := TonCanonicalBlockTransactionID(receive.StringRaw(), tx.LT, hex.EncodeToString(tx.Hash))
	if got != want {
		t.Fatalf("canonical id = %q, want %q", got, want)
	}
}

func TestValidateManualTonPaymentCandidatesUSDTJetton(t *testing.T) {
	receive := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	sender := address.NewAddress(0, 0, bytes.Repeat([]byte{0x68}, 32)).Bounce(false).Testnet(false)
	jettonWallet := address.NewAddress(0, 0, bytes.Repeat([]byte{0x69}, 32)).Bounce(false).Testnet(false)
	body, err := tlb.ToCell(jetton.TransferNotification{
		QueryID:        7,
		Amount:         tlb.MustFromNano(big.NewInt(1_000_000), 6),
		Sender:         sender,
		ForwardPayload: cell.BeginCell().EndCell(),
	})
	if err != nil {
		t.Fatalf("build jetton notification body: %v", err)
	}
	tx := tonTestInboundTx(t, jettonWallet, receive, tlb.FromNanoTONU(1), body)
	order := &mdb.Orders{
		BaseModel:      mdb.BaseModel{ID: 1, CreatedAt: *carbon.NewTime(carbon.CreateFromTimestampMilli(time.Now().Add(-time.Minute).UnixMilli()))},
		Network:        mdb.NetworkTon,
		Token:          "USDT",
		ActualAmount:   1,
		ReceiveAddress: receive.Bounce(false).String(),
	}
	state := &manualTonState{
		jettonWallets: map[string]mdb.ChainToken{
			addressutil.TonRawAddressObjectKey(jettonWallet): {
				BaseModel: mdb.BaseModel{ID: 2},
				Network:   mdb.NetworkTon,
				Symbol:    "USDT",
				Decimals:  6,
				Enabled:   true,
			},
		},
	}

	got, err := validateManualTonPaymentCandidates(order, receive, manualTonTxRef{
		LT:      tx.LT,
		HashHex: hex.EncodeToString(tx.Hash),
		HasLT:   true,
	}, []*tlb.Transaction{tx}, state)
	if err != nil {
		t.Fatalf("validateManualTonPaymentCandidates(): %v", err)
	}
	want := TonCanonicalBlockTransactionID(receive.StringRaw(), tx.LT, hex.EncodeToString(tx.Hash))
	if got != want {
		t.Fatalf("canonical id = %q, want %q", got, want)
	}
}

func TestValidateManualTonPaymentWithAPIRejectsAmbiguousHashOnly(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ? AND symbol = ?", mdb.NetworkTon, "USDT").
		Update("enabled", false).Error; err != nil {
		t.Fatalf("disable TON USDT token: %v", err)
	}

	receive := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	sender := address.NewAddress(0, 0, bytes.Repeat([]byte{0x67}, 32)).Bounce(false).Testnet(false)
	tx1 := tonTestInboundTx(t, sender, receive, tlb.MustFromTON("1.23"), nil)
	tx2 := tonTestInboundTx(t, sender, receive, tlb.MustFromTON("1.23"), nil)
	tx2.LT = tx1.LT + 1
	order := &mdb.Orders{
		BaseModel:      mdb.BaseModel{ID: 1, CreatedAt: *carbon.NewTime(carbon.CreateFromTimestampMilli(time.Now().Add(-time.Minute).UnixMilli()))},
		Network:        mdb.NetworkTon,
		Token:          "TON",
		ActualAmount:   1.23,
		ReceiveAddress: receive.Bounce(false).String(),
	}
	api := &fakeManualTonAPI{
		master: &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: -0x8000000000000000, SeqNo: 9},
		account: &tlb.Account{
			LastTxLT:   tx2.LT,
			LastTxHash: tx2.Hash,
		},
		txs: []*tlb.Transaction{tx1, tx2},
	}

	_, err := validateManualTonPaymentWithAPI(context.Background(), api, order, receive, manualTonTxRef{
		HashHex: hex.EncodeToString(tx1.Hash),
	})
	if err == nil || !strings.Contains(err.Error(), "matched multiple") {
		t.Fatalf("validate ambiguous hash-only error = %v, want multiple-match error", err)
	}
	if api.getAccountCalls != 1 {
		t.Fatalf("hash-only TON ref fetched account state %d times, want 1", api.getAccountCalls)
	}
	if len(api.listCalls) != 1 {
		t.Fatalf("ListTransactions calls = %#v, want one recent lookup", api.listCalls)
	}
	if call := api.listCalls[0]; call.limit != 100 || call.lt != tx2.LT || call.hashHex != hex.EncodeToString(tx2.Hash) {
		t.Fatalf("ListTransactions recent call = %#v, want account last tx cursor", call)
	}
}

func TestManualVerifyDialEvmClientsIncludesHTTPNode(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.RpcNode{
		Network: mdb.NetworkEthereum,
		Type:    mdb.RpcNodeTypeHttp,
		Url:     "http://127.0.0.1:1",
		Enabled: true,
		Status:  mdb.RpcNodeStatusOk,
	}).Error; err != nil {
		t.Fatalf("create rpc node: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	clients, err := dialManualEvmClients(ctx, mdb.NetworkEthereum)
	if err != nil {
		t.Fatalf("dialManualEvmClients(): %v", err)
	}
	defer closeManualEvmClients(clients)
	if len(clients) != 1 {
		t.Fatalf("client count = %d, want 1", len(clients))
	}
	if !strings.Contains(clients[0].label, mdb.RpcNodeTypeHttp) {
		t.Fatalf("client label = %q, want HTTP node", clients[0].label)
	}
}

func TestManualVerifyEvmCandidatesPreferGeneralAcrossNodeTypes(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	rows := []mdb.RpcNode{
		{Network: mdb.NetworkEthereum, Type: mdb.RpcNodeTypeHttp, Url: "http://manual.example.com", Weight: 100, Enabled: true, Purpose: mdb.RpcNodePurposeManualVerify, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkEthereum, Type: mdb.RpcNodeTypeWs, Url: "ws://general.example.com", Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
	}
	if err := dao.Mdb.Create(&rows).Error; err != nil {
		t.Fatalf("seed rpc_nodes: %v", err)
	}

	got, err := listManualEvmRpcCandidates(mdb.NetworkEthereum)
	if err != nil {
		t.Fatalf("listManualEvmRpcCandidates(): %v", err)
	}
	urls := make([]string, 0, len(got))
	for _, node := range got {
		urls = append(urls, node.Url)
	}
	want := []string{"ws://general.example.com", "http://manual.example.com"}
	if len(urls) != len(want) {
		t.Fatalf("candidate urls = %#v, want %#v", urls, want)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("candidate urls = %#v, want %#v", urls, want)
		}
	}
}

func TestManualRpcNodeLabelHidesSensitiveURLParts(t *testing.T) {
	label := manualRpcNodeLabel(mdb.RpcNode{
		BaseModel: mdb.BaseModel{ID: 9},
		Network:   mdb.NetworkEthereum,
		Type:      mdb.RpcNodeTypeHttp,
		Url:       "https://rpc.example.com/v3/secret-key?token=secret",
		Purpose:   mdb.RpcNodePurposeManualVerify,
	})
	if strings.Contains(label, "secret") || strings.Contains(label, "/v3/") {
		t.Fatalf("manualRpcNodeLabel() leaked sensitive URL parts: %s", label)
	}
	if !strings.Contains(label, "endpoint=https://rpc.example.com") {
		t.Fatalf("manualRpcNodeLabel() = %s, want sanitized endpoint", label)
	}
	if !strings.Contains(label, "purpose=manual_verify") {
		t.Fatalf("manualRpcNodeLabel() = %s, want purpose", label)
	}
}

func TestManualVerifyDialEvmClientDoesNotForwardUserIPHeader(t *testing.T) {
	var headerCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-USER-IP"); got != "" {
			t.Errorf("X-USER-IP header = %q, want empty", got)
			http.Error(w, "unexpected user ip header", http.StatusBadRequest)
			return
		}
		atomic.AddInt32(&headerCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]interface{}{
				"code":    -32000,
				"message": "not found",
			},
		})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := dialManualEvmClient(ctx, mdb.RpcNode{
		Network: mdb.NetworkEthereum,
		Type:    mdb.RpcNodeTypeHttp,
		Url:     server.URL,
		Purpose: mdb.RpcNodePurposeManualVerify,
	})
	if err != nil {
		t.Fatalf("dialManualEvmClient(): %v", err)
	}
	defer client.Close()

	_, _ = client.TransactionReceipt(ctx, common.HexToHash("0x"+strings.Repeat("a", 64)))
	if got := atomic.LoadInt32(&headerCalls); got == 0 {
		t.Fatal("manual verify EVM request did not reach test RPC")
	}
}

func TestManualVerifyDialEvmClientDoesNotForwardUserIPForGeneralNode(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-USER-IP"); got != "" {
			t.Errorf("X-USER-IP header = %q, want empty", got)
			http.Error(w, "unexpected user ip header", http.StatusBadRequest)
			return
		}
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]interface{}{
				"code":    -32000,
				"message": "not found",
			},
		})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := dialManualEvmClient(ctx, mdb.RpcNode{
		Network: mdb.NetworkEthereum,
		Type:    mdb.RpcNodeTypeHttp,
		Url:     server.URL,
		Purpose: mdb.RpcNodePurposeGeneral,
	})
	if err != nil {
		t.Fatalf("dialManualEvmClient(): %v", err)
	}
	defer client.Close()

	_, _ = client.TransactionReceipt(ctx, common.HexToHash("0x"+strings.Repeat("a", 64)))
	if got := atomic.LoadInt32(&calls); got == 0 {
		t.Fatal("general EVM request did not reach test RPC")
	}
}

func TestTronPostJSONDoesNotForwardUserIPWithoutHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-USER-IP"); got != "" {
			t.Errorf("X-USER-IP header = %q, want empty", got)
			http.Error(w, "unexpected user ip header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	var out map[string]interface{}
	if err := tronPostJSON(server.URL, "", "/wallet/getnowblock", map[string]interface{}{}, &out); err != nil {
		t.Fatalf("tronPostJSON(): %v", err)
	}
}

func TestManualVerifyEvmRejectsTransactionBeforeOrder(t *testing.T) {
	order := &mdb.Orders{BaseModel: mdb.BaseModel{CreatedAt: *carbon.NewTime(carbon.CreateFromTimestampMilli(time.Now().UnixMilli()))}}
	txTime := uint64(time.Now().Add(-time.Hour).Unix())
	if err := ensureEvmTransactionNotBeforeOrder(txTime, order); err == nil {
		t.Fatal("expected transaction before order to be rejected")
	}
	txTime = uint64(time.Now().Add(time.Minute).Unix())
	if err := ensureEvmTransactionNotBeforeOrder(txTime, order); err != nil {
		t.Fatalf("expected transaction after order to pass: %v", err)
	}
}

func TestManualVerifyCanonicalEvmHash(t *testing.T) {
	hash := strings.Repeat("a", 64)
	_, canonical, err := canonicalEvmHash("0X" + strings.ToUpper(hash))
	if err != nil {
		t.Fatalf("canonicalEvmHash(): %v", err)
	}
	if canonical != "0x"+hash {
		t.Fatalf("canonical hash = %q, want %q", canonical, "0x"+hash)
	}
}

func TestManualVerifyEquivalentBlockIDsCatchLegacyVariants(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	hash := strings.Repeat("b", 64)
	mixedHash := "0x" + strings.Repeat("bB", 32)
	if err := dao.Mdb.Create(&mdb.Orders{
		TradeId:            "paid-legacy-hash",
		OrderId:            "paid-legacy-hash",
		Status:             mdb.StatusPaySuccess,
		Network:            mdb.NetworkEthereum,
		BlockTransactionId: mixedHash,
	}).Error; err != nil {
		t.Fatalf("create existing order: %v", err)
	}

	order := &mdb.Orders{BaseModel: mdb.BaseModel{ID: 999}, Network: mdb.NetworkEthereum}
	if err := ensureManualBlockTransactionUnused(order, "0x"+hash); err == nil {
		t.Fatal("expected legacy hash variant to be treated as already processed")
	}
}

func TestManualVerifyEvmRequestFailureFallsBackToNextClient(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	contract := common.HexToAddress("0x1111111111111111111111111111111111111111")
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	rawAmount := big.NewInt(1230000)
	receipt := &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(10),
		Logs: []*types.Log{{
			Address: contract,
			Topics: []common.Hash{
				erc20TransferEventHash,
				common.Hash{},
				common.BytesToHash(to.Bytes()),
			},
			Data: common.LeftPadBytes(rawAmount.Bytes(), 32),
		}},
	}
	order := &mdb.Orders{
		Network:        mdb.NetworkEthereum,
		Token:          "USDT",
		ActualAmount:   1.23,
		ReceiveAddress: to.Hex(),
		BaseModel:      mdb.BaseModel{CreatedAt: *carbon.NewTime(carbon.CreateFromTimestampMilli(time.Now().Add(-time.Minute).UnixMilli()))},
	}
	token := &mdb.ChainToken{Network: mdb.NetworkEthereum, Symbol: "USDT", ContractAddress: contract.Hex(), Decimals: 6}

	err := validateManualEvmPaymentAcrossClients(context.Background(), []manualEvmClient{
		{label: "ws bad", reader: &fakeEvmReader{receiptErr: errors.New("ws receipt failed")}},
		{label: "http ok", reader: &fakeEvmReader{
			receipt: receipt,
			headers: map[string]*types.Header{
				"10":     {Number: big.NewInt(10), Time: uint64(time.Now().Unix())},
				"latest": {Number: big.NewInt(12), Time: uint64(time.Now().Unix())},
			},
		}},
	}, order, common.HexToHash("0x"+strings.Repeat("c", 64)), token)
	if err != nil {
		t.Fatalf("validateManualEvmPaymentAcrossClients(): %v", err)
	}
}

func TestManualVerifyTronTRC20UsesTransferEvent(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	contractHex := "411111111111111111111111111111111111111111"
	recipientHex := "41a614f803b6fd780986a42c78ec9c7f77e6ded13c"
	contractAddress, err := tronHexToAddress(contractHex)
	if err != nil {
		t.Fatalf("contract address: %v", err)
	}
	recipientAddress, err := tronHexToAddress(recipientHex)
	if err != nil {
		t.Fatalf("recipient address: %v", err)
	}
	upsertTestChainToken(t, mdb.ChainToken{
		Network:         mdb.NetworkTron,
		Symbol:          "USDT",
		ContractAddress: contractAddress,
		Decimals:        6,
		Enabled:         true,
	})

	rawAmount := big.NewInt(1230000)
	tx := manualTronTransactionFromCallData(t, contractHex, recipientHex, rawAmount)
	info := &manualTronTxInfo{Log: []manualTronEventLog{{
		Address: strings.TrimPrefix(contractHex, "41"),
		Topics: []string{
			"0x" + strings.TrimPrefix(erc20TransferEventHash.Hex(), "0x"),
			strings.Repeat("0", 64),
			"0x" + strings.Repeat("0", 24) + strings.TrimPrefix(recipientHex, "41"),
		},
		Data: "0x" + fmt.Sprintf("%064x", rawAmount),
	}}}
	order := &mdb.Orders{ReceiveAddress: recipientAddress, Token: "USDT", ActualAmount: 1.23}

	if err = validateManualTronTRC20Transfer(order, &tx, info); err != nil {
		t.Fatalf("validateManualTronTRC20Transfer(): %v", err)
	}

	info.Log[0].Data = "0x" + fmt.Sprintf("%064x", big.NewInt(1240000))
	if err = validateManualTronTRC20Transfer(order, &tx, info); err == nil {
		t.Fatal("expected event amount mismatch to fail")
	}
}

func TestManualVerifyTronPaymentHTTPFlow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	txID := strings.Repeat("a", 64)
	contractHex := "411111111111111111111111111111111111111111"
	recipientHex := "41a614f803b6fd780986a42c78ec9c7f77e6ded13c"
	contractAddress, err := tronHexToAddress(contractHex)
	if err != nil {
		t.Fatalf("contract address: %v", err)
	}
	recipientAddress, err := tronHexToAddress(recipientHex)
	if err != nil {
		t.Fatalf("recipient address: %v", err)
	}

	rawAmount := big.NewInt(1230000)
	blockTimeMs := time.Now().Add(time.Minute).UnixMilli()
	requestCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-USER-IP"); got != "" {
			t.Errorf("X-USER-IP header = %q, want empty", got)
		}
		requestCalls++
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode tron request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/wallet/getnowblock" && req["value"] != txID {
			t.Errorf("request tx id = %q, want %q", req["value"], txID)
			http.Error(w, "bad tx id", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/gettransactionbyid":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"txID": txID,
				"raw_data": map[string]interface{}{
					"contract": []map[string]interface{}{{
						"type": "TriggerSmartContract",
						"parameter": map[string]interface{}{
							"value": map[string]interface{}{},
						},
					}},
				},
				"ret": []map[string]string{{"contractRet": "SUCCESS"}},
			})
		case "/wallet/gettransactioninfobyid":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             txID,
				"blockNumber":    100,
				"blockTimeStamp": blockTimeMs,
				"receipt":        map[string]string{"result": "SUCCESS"},
				"log": []map[string]interface{}{{
					"address": strings.TrimPrefix(contractHex, "41"),
					"topics": []string{
						strings.TrimPrefix(erc20TransferEventHash.Hex(), "0x"),
						strings.Repeat("0", 64),
						"0X" + strings.ToUpper(strings.Repeat("0", 24)+strings.TrimPrefix(recipientHex, "41")),
					},
					"data": fmt.Sprintf("%064x", rawAmount),
				}},
			})
		case "/wallet/getnowblock":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"block_header": map[string]interface{}{
					"raw_data": map[string]interface{}{"number": 110},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err = dao.Mdb.Create(&mdb.RpcNode{
		Network: mdb.NetworkTron,
		Type:    mdb.RpcNodeTypeHttp,
		Url:     server.URL,
		Enabled: true,
		Status:  mdb.RpcNodeStatusOk,
		Purpose: mdb.RpcNodePurposeManualVerify,
	}).Error; err != nil {
		t.Fatalf("create tron rpc node: %v", err)
	}
	upsertTestChainToken(t, mdb.ChainToken{
		Network:         mdb.NetworkTron,
		Symbol:          "USDT",
		ContractAddress: contractAddress,
		Decimals:        6,
		Enabled:         true,
	})
	order := &mdb.Orders{
		TradeId:        "manual-tron-http-flow",
		OrderId:        "manual-tron-http-flow",
		ActualAmount:   1.23,
		ReceiveAddress: recipientAddress,
		Token:          "USDT",
		Network:        mdb.NetworkTron,
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	if err = dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	got, err := ValidateManualOrderPayment(order, "0X"+strings.ToUpper(txID))
	if err != nil {
		t.Fatalf("ValidateManualOrderPayment(): %v", err)
	}
	if got != txID {
		t.Fatalf("canonical tx id = %q, want %q", got, txID)
	}
	if requestCalls != 3 {
		t.Fatalf("manual verify request calls = %d, want 3", requestCalls)
	}
}

func TestManualVerifySolanaRejectsMissingBlockTime(t *testing.T) {
	order := &mdb.Orders{BaseModel: mdb.BaseModel{CreatedAt: *carbon.NewTime(carbon.CreateFromTimestampMilli(time.Now().UnixMilli()))}}
	if err := ensureSolanaTransferNotBeforeOrder(0, order); err == nil {
		t.Fatal("expected missing block time to fail")
	}
	if err := ensureSolanaTransferNotBeforeOrder(time.Now().Add(time.Minute).Unix(), order); err != nil {
		t.Fatalf("expected future block time to pass: %v", err)
	}
}

type fakeEvmReader struct {
	receipt    *types.Receipt
	receiptErr error
	headers    map[string]*types.Header
	headerErr  error
}

func (f *fakeEvmReader) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	if f.receiptErr != nil {
		return nil, f.receiptErr
	}
	return f.receipt, nil
}

func (f *fakeEvmReader) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if f.headerErr != nil {
		return nil, f.headerErr
	}
	key := "latest"
	if number != nil {
		key = number.String()
	}
	header := f.headers[key]
	if header == nil {
		return nil, fmt.Errorf("missing header %s", key)
	}
	return header, nil
}

func manualTronTransactionFromCallData(t *testing.T, contractHex, recipientHex string, amount *big.Int) manualTronTransaction {
	t.Helper()
	body := strings.TrimPrefix(recipientHex, "41")
	raw := fmt.Sprintf(`{
		"raw_data": {
			"contract": [{
				"type": "TriggerSmartContract",
				"parameter": {
					"value": {
						"contract_address": %q,
						"data": %q
					}
				}
			}]
		}
	}`, contractHex, "a9059cbb"+strings.Repeat("0", 24)+body+fmt.Sprintf("%064x", amount))
	var tx manualTronTransaction
	if err := json.Unmarshal([]byte(raw), &tx); err != nil {
		t.Fatalf("unmarshal tron tx: %v", err)
	}
	return tx
}
