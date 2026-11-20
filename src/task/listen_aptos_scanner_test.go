package task

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
)

type fakeAptosProvider struct {
	mu sync.Mutex

	latest int64

	bodiesByStart map[int64][]byte
	errByStart    map[int64]error

	calls []aptosRangeCall
}

type aptosRangeCall struct {
	start int64
	limit int64
}

func (f *fakeAptosProvider) LatestLedgerVersion() (int64, error) {
	return f.latest, nil
}

func (f *fakeAptosProvider) Transactions(start int64, limit int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, aptosRangeCall{start: start, limit: limit})
	if err := f.errByStart[start]; err != nil {
		return nil, err
	}
	if body := f.bodiesByStart[start]; len(body) > 0 {
		return append([]byte(nil), body...), nil
	}
	return []byte("[]"), nil
}

func (f *fakeAptosProvider) rangeCalls() []aptosRangeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := append([]aptosRangeCall(nil), f.calls...)
	sort.Slice(calls, func(i, j int) bool {
		if calls[i].start == calls[j].start {
			return calls[i].limit < calls[j].limit
		}
		return calls[i].start < calls[j].start
	})
	return calls
}

func resetAptosScannerHooks(t *testing.T) {
	t.Helper()
	oldParse := parseAptosTransfersForWallets
	oldProcess := processAptosObservedTransferFunc
	t.Cleanup(func() {
		parseAptosTransfersForWallets = oldParse
		processAptosObservedTransferFunc = oldProcess
	})
}

func seedAptosScannerChain(t *testing.T, wallets ...string) []string {
	t.Helper()
	if err := dao.Mdb.Create(&mdb.Chain{Network: mdb.NetworkAptos, Enabled: true, MinConfirmations: 1, ScanIntervalSec: 1}).Error; err != nil {
		t.Fatalf("seed Aptos chain: %v", err)
	}
	if err := dao.Mdb.Create(&[]mdb.ChainToken{
		{Network: mdb.NetworkAptos, Symbol: "USDC", ContractAddress: "0xbae207659db88bea0cbead6da0ed00aac12edcdda169e591cd41c94180b46f3b", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkAptos, Symbol: "USDT", ContractAddress: "0x357b0b74bc833e95a115ad22604854d6b0fca151cecd94111770e5d6ffc9dc2b", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkAptos, Symbol: "MOVEUSD", ContractAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Decimals: 6, Enabled: true},
	}).Error; err != nil {
		t.Fatalf("seed Aptos tokens: %v", err)
	}
	if len(wallets) == 0 {
		wallets = []string{"0xa"}
	}
	out := make([]string, 0, len(wallets))
	for _, wallet := range wallets {
		receive, err := addressutil.NormalizeMoveAddress(wallet)
		if err != nil {
			t.Fatalf("normalize Aptos address %q: %v", wallet, err)
		}
		if _, err = data.AddWalletAddressWithNetwork(mdb.NetworkAptos, receive); err != nil {
			t.Fatalf("seed Aptos wallet %s: %v", receive, err)
		}
		out = append(out, receive)
	}
	return out
}

func TestProcessAptosLedgerRangePassesConfiguredTokens(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	seedAptosScannerChain(t, "0xa")

	state, err := loadMoveWatchState(mdb.NetworkAptos)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	var gotSymbols []string
	parseAptosTransfersForWallets = func(_ []byte, _ map[string]struct{}, tokens []mdb.ChainToken) ([]service.MoveObservedTransfer, error) {
		for _, token := range tokens {
			gotSymbols = append(gotSymbols, token.Symbol)
		}
		sort.Strings(gotSymbols)
		return nil, nil
	}
	provider := &fakeAptosProvider{bodiesByStart: map[int64][]byte{100: []byte("[]")}}

	err = processAptosLedgerRange(context.Background(), provider, aptosLedgerRange{start: 100, limit: 1, state: state}, 100)
	if err != nil {
		t.Fatalf("processAptosLedgerRange(): %v", err)
	}
	want := []string{"MOVEUSD", "USDC", "USDT"}
	if len(gotSymbols) != len(want) {
		t.Fatalf("tokens = %#v, want %#v", gotSymbols, want)
	}
	for i := range want {
		if gotSymbols[i] != want[i] {
			t.Fatalf("tokens = %#v, want %#v", gotSymbols, want)
		}
	}
}

func TestRunAptosLedgerScannerIdlesWhenChainDisabled(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	if err := dao.Mdb.Create(&mdb.Chain{Network: mdb.NetworkAptos, Enabled: false, MinConfirmations: 1, ScanIntervalSec: 1}).Error; err != nil {
		t.Fatalf("seed disabled Aptos chain: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	provider := &fakeAptosProvider{latest: 999}
	cursor := &aptosRuntimeCursor{}
	if err := runAptosLedgerScanner(ctx, provider, cursor); err != nil {
		t.Fatalf("runAptosLedgerScanner(): %v", err)
	}
	if cursor.initialized {
		t.Fatalf("cursor initialized while chain disabled: %#v", cursor)
	}
	if calls := provider.rangeCalls(); len(calls) != 0 {
		t.Fatalf("range calls = %#v, want none", calls)
	}
}

func TestRunAptosLedgerScannerInitializesAtTrailingConfirmedChunk(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	seedAptosScannerChain(t, "0xa")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	provider := &fakeAptosProvider{latest: 999}
	cursor := &aptosRuntimeCursor{}
	if err := runAptosLedgerScanner(ctx, provider, cursor); err != nil {
		t.Fatalf("runAptosLedgerScanner(): %v", err)
	}

	if !cursor.initialized || cursor.lastSeenVersion != 999 {
		t.Fatalf("cursor = %#v, want initialized and advanced to confirmed head 999", cursor)
	}
	calls := provider.rangeCalls()
	if len(calls) != 1 || calls[0].start != 900 || calls[0].limit != 100 {
		t.Fatalf("range calls = %#v, want start=900 limit=100", calls)
	}
}

func TestProcessAptosLedgerRoundDoesNotAdvanceUnconfirmedVersion(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	seedAptosScannerChain(t, "0xa")

	provider := &fakeAptosProvider{latest: 100}
	cursor := &aptosRuntimeCursor{initialized: true, lastSeenVersion: 99}
	state, err := loadMoveWatchState(mdb.NetworkAptos)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	catchup, err := processAptosLedgerRound(context.Background(), provider, state, cursor, 99)
	if err != nil {
		t.Fatalf("processAptosLedgerRound(): %v", err)
	}
	if catchup {
		t.Fatal("catchup = true, want false")
	}
	if cursor.lastSeenVersion != 99 {
		t.Fatalf("lastSeenVersion = %d, want 99", cursor.lastSeenVersion)
	}
	if calls := provider.rangeCalls(); len(calls) != 0 {
		t.Fatalf("range calls = %#v, want none", calls)
	}
}

func TestProcessAptosLedgerRoundAdvancesAfterSuccessfulRange(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	seedAptosScannerChain(t, "0xa")

	state, err := loadMoveWatchState(mdb.NetworkAptos)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	provider := &fakeAptosProvider{bodiesByStart: map[int64][]byte{100: []byte("[]")}}
	cursor := &aptosRuntimeCursor{initialized: true, lastSeenVersion: 99}

	catchup, err := processAptosLedgerRound(context.Background(), provider, state, cursor, 250)
	if err != nil {
		t.Fatalf("processAptosLedgerRound(): %v", err)
	}
	if catchup {
		t.Fatal("catchup = true, want false")
	}
	if cursor.lastSeenVersion != 250 {
		t.Fatalf("lastSeenVersion = %d, want 250", cursor.lastSeenVersion)
	}
	calls := provider.rangeCalls()
	if len(calls) != 2 || calls[0].start != 100 || calls[0].limit != 100 || calls[1].start != 200 || calls[1].limit != 51 {
		t.Fatalf("range calls = %#v, want [100/100 200/51]", calls)
	}
}

func TestProcessAptosLedgerRoundDoesNotAdvanceOnRangeFailure(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	seedAptosScannerChain(t, "0xa")

	state, err := loadMoveWatchState(mdb.NetworkAptos)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	provider := &fakeAptosProvider{errByStart: map[int64]error{100: errors.New("temporary rpc failure")}}
	cursor := &aptosRuntimeCursor{initialized: true, lastSeenVersion: 99}

	if _, err = processAptosLedgerRound(context.Background(), provider, state, cursor, 150); err == nil {
		t.Fatal("processAptosLedgerRound() error = nil, want error")
	}
	if cursor.lastSeenVersion != 99 {
		t.Fatalf("lastSeenVersion = %d, want 99", cursor.lastSeenVersion)
	}
}

func TestProcessAptosLedgerRoundProcessesMultipleChunks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	seedAptosScannerChain(t, "0xa")

	state, err := loadMoveWatchState(mdb.NetworkAptos)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	provider := &fakeAptosProvider{
		bodiesByStart: map[int64][]byte{
			100: []byte("[]"),
			200: []byte("[]"),
		},
	}
	cursor := &aptosRuntimeCursor{initialized: true, lastSeenVersion: 99}

	catchup, err := processAptosLedgerRound(context.Background(), provider, state, cursor, 250)
	if err != nil {
		t.Fatalf("processAptosLedgerRound(): %v", err)
	}
	if catchup {
		t.Fatal("catchup = true, want false")
	}
	if cursor.lastSeenVersion != 250 {
		t.Fatalf("lastSeenVersion = %d, want 250", cursor.lastSeenVersion)
	}
	calls := provider.rangeCalls()
	if len(calls) != 2 || calls[0].start != 100 || calls[0].limit != 100 || calls[1].start != 200 || calls[1].limit != 51 {
		t.Fatalf("range calls = %#v, want [100/100 200/51]", calls)
	}
}

func TestBuildAptosLedgerRangesStopsAtQueueLimit(t *testing.T) {
	state := moveWatchState{}
	ranges := buildAptosLedgerRanges(0, aptosLedgerChunkSize*aptosLedgerQueueLimit+500, state)
	if len(ranges) != aptosLedgerQueueLimit {
		t.Fatalf("ranges len = %d, want %d", len(ranges), aptosLedgerQueueLimit)
	}
	first := ranges[0]
	last := ranges[len(ranges)-1]
	if first.start != 1 || first.limit != aptosLedgerChunkSize {
		t.Fatalf("first range = %#v, want start=1 limit=%d", first, aptosLedgerChunkSize)
	}
	if last.start != 1+aptosLedgerChunkSize*int64(aptosLedgerQueueLimit-1) || last.limit != aptosLedgerChunkSize {
		t.Fatalf("last range = %#v", last)
	}
}

func TestProcessAptosLedgerRoundMarksMatchingUSDTOrderPaid(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	wallets := seedAptosScannerChain(t, "0xa")
	receive := wallets[0]
	amount := 3.1
	tradeID := "aptos_trade_1"
	usdt := "0x357b0b74bc833e95a115ad22604854d6b0fca151cecd94111770e5d6ffc9dc2b"
	body := aptosFungibleTransferBody(t, "0xabc", 101, receive, usdt, "3100000")

	order := &mdb.Orders{
		TradeId:         tradeID,
		OrderId:         "aptos_order_1",
		Amount:          amount,
		ActualAmount:    amount,
		ReceiveAddress:  receive,
		Token:           "USDT",
		Network:         mdb.NetworkAptos,
		Status:          mdb.StatusWaitPay,
		CallBackConfirm: mdb.CallBackConfirmOk,
		PaymentType:     mdb.PaymentTypeGmpay,
		PayProvider:     mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := data.LockTransaction(mdb.NetworkAptos, receive, "USDT", tradeID, amount, time.Hour); err != nil {
		t.Fatalf("lock transaction: %v", err)
	}

	state, err := loadMoveWatchState(mdb.NetworkAptos)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	provider := &fakeAptosProvider{bodiesByStart: map[int64][]byte{101: body}}
	cursor := &aptosRuntimeCursor{initialized: true, lastSeenVersion: 100}

	if _, err = processAptosLedgerRound(context.Background(), provider, state, cursor, 101); err != nil {
		t.Fatalf("processAptosLedgerRound(): %v", err)
	}
	paid, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if paid.Status != mdb.StatusPaySuccess || paid.CallBackConfirm != mdb.CallBackConfirmNo || paid.BlockTransactionId != "0xabc" {
		t.Fatalf("paid order = %#v", paid)
	}
	lockTradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkAptos, receive, "USDT", amount)
	if err != nil {
		t.Fatalf("lookup lock: %v", err)
	}
	if lockTradeID != "" {
		t.Fatalf("transaction lock still exists: %s", lockTradeID)
	}
}

func TestProcessAptosLedgerRoundDoesNotReprocessDuplicateTransferKey(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	resetAptosScannerHooks(t)
	wallets := seedAptosScannerChain(t, "0xa")
	receive := wallets[0]
	usdc := "0xbae207659db88bea0cbead6da0ed00aac12edcdda169e591cd41c94180b46f3b"

	state, err := loadMoveWatchState(mdb.NetworkAptos)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	provider := &fakeAptosProvider{
		bodiesByStart: map[int64][]byte{
			101: aptosFungibleTransferBody(t, "0xabc", 101, receive, usdc, "1200000"),
			102: aptosFungibleTransferBody(t, "0xabc", 101, receive, usdc, "1200000"),
		},
	}
	var processed int
	processAptosObservedTransferFunc = func(transfer service.MoveObservedTransfer) error {
		processed++
		return service.ProcessMoveObservedTransferResult(transfer)
	}
	cursor := &aptosRuntimeCursor{initialized: true, lastSeenVersion: 100}

	if _, err = processAptosLedgerRound(context.Background(), provider, state, cursor, 101); err != nil {
		t.Fatalf("first processAptosLedgerRound(): %v", err)
	}
	if _, err = processAptosLedgerRound(context.Background(), provider, state, cursor, 102); err != nil {
		t.Fatalf("second processAptosLedgerRound(): %v", err)
	}
	if processed != 2 {
		t.Fatalf("scanner processed callback count = %d, want 2", processed)
	}
	if cursor.lastSeenVersion != 102 {
		t.Fatalf("lastSeenVersion = %d, want 102", cursor.lastSeenVersion)
	}
}

func aptosFungibleTransferBody(t *testing.T, hash string, version int64, receive string, metadata string, rawAmount string) []byte {
	t.Helper()
	store, err := addressutil.NormalizeMoveAddress(fmt.Sprintf("0x%x", version+1000))
	if err != nil {
		t.Fatalf("normalize store: %v", err)
	}
	senderStore, err := addressutil.NormalizeMoveAddress(fmt.Sprintf("0x%x", version+2000))
	if err != nil {
		t.Fatalf("normalize sender store: %v", err)
	}
	return []byte(`[
		{
			"type":"user_transaction",
			"success":true,
			"hash":"` + hash + `",
			"version":"` + fmt.Sprintf("%d", version) + `",
			"timestamp":"` + fmt.Sprintf("%d", time.Now().UnixMicro()) + `",
			"events":[
				{"type":"0x1::fungible_asset::Withdraw","data":{"amount":"` + rawAmount + `","store":"` + senderStore + `"}},
				{"type":"0x1::fungible_asset::Deposit","data":{"amount":"` + rawAmount + `","store":"` + store + `"}}
			],
			"changes":[
				{"address":"` + senderStore + `","data":{"type":"0x1::fungible_asset::FungibleStore","data":{"metadata":{"inner":"` + metadata + `"}}},"type":"write_resource"},
				{"address":"` + store + `","data":{"type":"0x1::fungible_asset::FungibleStore","data":{"metadata":{"inner":"` + metadata + `"}}},"type":"write_resource"},
				{"address":"` + store + `","data":{"type":"0x1::object::ObjectCore","data":{"owner":"` + receive + `"}},"type":"write_resource"}
			]
		}
	]`)
}
