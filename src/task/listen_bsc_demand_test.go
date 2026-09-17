package task

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/dromara/carbon/v2"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

var bscTestRecipient = common.HexToAddress("0x1111111111111111111111111111111111111111")
var bscTestContract = common.HexToAddress("0x55d398326f99059fF775485246999027B3197955")

type bscDemandRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newBscDemandHTTPServer(t *testing.T, handle func(string, json.RawMessage) (interface{}, *bscDemandRPCError)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		result, rpcErr := handle(req.Method, req.Params)
		response := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

type bscTestRPC struct {
	mu           sync.Mutex
	base         time.Time
	head         int64
	headers      int
	scans        [][2]int64
	failLogs     bool
	failHeaderAt int64
	logs         []types.Log
}

func (f *bscTestRPC) header(n int64) *types.Header {
	return &types.Header{Number: big.NewInt(n), Difficulty: big.NewInt(1), Time: uint64(f.base.Unix() + n/2), GasLimit: 30000000}
}

func (f *bscTestRPC) handle(method string, raw json.RawMessage) (interface{}, *bscDemandRPCError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var params []json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &bscDemandRPCError{Code: -32602, Message: err.Error()}
	}
	switch method {
	case "eth_getBlockByNumber":
		f.headers++
		var block string
		_ = json.Unmarshal(params[0], &block)
		n := f.head
		if block != "latest" {
			n, _ = strconv.ParseInt(block[2:], 16, 64)
		}
		if n == f.failHeaderAt {
			return nil, &bscDemandRPCError{Code: -32000, Message: "header unavailable"}
		}
		return f.header(n), nil
	case "eth_getLogs":
		// Only decode block bounds; address and topics are arrays.
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(params[0], &fields)
		q := make(map[string]string)
		for _, k := range []string{"fromBlock", "toBlock"} {
			var value string
			_ = json.Unmarshal(fields[k], &value)
			q[k] = value
		}
		from, _ := strconv.ParseInt(q["fromBlock"][2:], 16, 64)
		to, _ := strconv.ParseInt(q["toBlock"][2:], 16, 64)
		f.scans = append(f.scans, [2]int64{from, to})
		if f.failLogs {
			return nil, &bscDemandRPCError{Code: 429, Message: "monthly capacity limit exceeded"}
		}
		out := make([]types.Log, 0)
		for _, log := range f.logs {
			if int64(log.BlockNumber) >= from && int64(log.BlockNumber) <= to {
				out = append(out, log)
			}
		}
		return out, nil
	default:
		return nil, &bscDemandRPCError{Code: -32601, Message: "unexpected method: " + method}
	}
}

func newBscDemandTest(t *testing.T) (*bscTestRPC, *ethclient.Client) {
	t.Helper()
	cleanup := testutil.SetupTestDatabases(t)
	t.Cleanup(cleanup)
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)
	data.ResetRpcRuntimeStatsForTest()
	t.Cleanup(data.ResetRpcRuntimeStatsForTest)
	f := &bscTestRPC{base: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), head: 10000, failHeaderAt: -1}
	server := newBscDemandHTTPServer(t, f.handle)
	t.Cleanup(server.Close)
	client, err := ethclient.Dial(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if err = dao.Mdb.Create(&mdb.WalletAddress{Network: mdb.NetworkBsc, Address: bscTestRecipient.Hex(), Status: mdb.TokenStatusEnable}).Error; err != nil {
		t.Fatal(err)
	}
	return f, client
}

func seedBscDemandOrder(t *testing.T, created time.Time, status int) *mdb.Orders {
	t.Helper()
	key := fmt.Sprintf("order-%d", created.UnixNano())
	o := &mdb.Orders{TradeId: key, OrderId: key, Network: mdb.NetworkBsc, Token: "USDT", ReceiveAddress: bscTestRecipient.Hex(), ActualAmount: 1, Amount: 1, Currency: "USD", Status: status, PayProvider: mdb.PaymentProviderOnChain, BaseModel: mdb.BaseModel{CreatedAt: *carbon.NewTime(carbon.CreateFromStdTime(created))}}
	if err := dao.Mdb.Create(o).Error; err != nil {
		t.Fatal(err)
	}
	return o
}

func bscDemandStep(t *testing.T, c *ethclient.Client, p *bscBackfillProgress, now time.Time) (time.Duration, error) {
	t.Helper()
	return runBscDemandBackfillStep(context.Background(), c, mdb.RpcNode{BaseModel: mdb.BaseModel{ID: 900}}, ethereum.FilterQuery{Addresses: []common.Address{bscTestContract}}, func(a common.Address) bool { return a == bscTestRecipient }, p, now)
}

func TestBscDemandIdleDoesNotCallRPC(t *testing.T) {
	f, c := newBscDemandTest(t)
	var p bscBackfillProgress
	for i := 0; i < 10; i++ {
		if _, err := bscDemandStep(t, c, &p, f.base.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if f.headers != 0 || len(f.scans) != 0 {
		t.Fatalf("idle RPC calls: headers=%d logs=%v", f.headers, f.scans)
	}
	row, err := data.GetEvmScanCursor(mdb.NetworkBsc)
	if err != nil || row.ID != 0 {
		t.Fatalf("idle created cursor: %+v %v", row, err)
	}
}

func TestBscDemandSkipsEmptyGapButCoversNewOrder(t *testing.T) {
	f, c := newBscDemandTest(t)
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 100); err != nil {
		t.Fatal(err)
	}
	created := f.base.Add(4500 * time.Second)
	seedBscDemandOrder(t, created, mdb.StatusWaitPay)
	var p bscBackfillProgress
	_, err := bscDemandStep(t, c, &p, created.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// Window begins at second 4380, blocks 8760 and 8761 share it.
	if len(f.scans) != 1 || f.scans[0] != [2]int64{8760, 8769} {
		t.Fatalf("range=%v", f.scans)
	}
	if f.headers > 25 {
		t.Fatalf("seeking empty gap used %d headers", f.headers)
	}
	row, _ := data.GetEvmScanCursor(mdb.NetworkBsc)
	if row.LastBlock != 8769 {
		t.Fatalf("cursor=%d", row.LastBlock)
	}
}

func TestBscDemandFailedQueryAndRestartRetainUnscannedRange(t *testing.T) {
	f, c := newBscDemandTest(t)
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 100); err != nil {
		t.Fatal(err)
	}
	created := f.base.Add(4500 * time.Second)
	seedBscDemandOrder(t, created, mdb.StatusExpired)
	f.failLogs = true
	var p bscBackfillProgress
	if _, err := bscDemandStep(t, c, &p, created.Add(7*24*time.Hour)); err == nil {
		t.Fatal("expected RPC failure")
	}
	row, _ := data.GetEvmScanCursor(mdb.NetworkBsc)
	if row.LastBlock != 100 || p.lastBlock != 100 {
		t.Fatalf("advanced on failure: db=%d memory=%d", row.LastBlock, p.lastBlock)
	}
	f.failLogs = false
	p = bscBackfillProgress{} // process/node restart
	if _, err := bscDemandStep(t, c, &p, created.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(f.scans) != 2 || f.scans[0] != f.scans[1] {
		t.Fatalf("restart skipped failed batch: %v", f.scans)
	}
}

func TestBscDemandExpiredWindowBecomesIdleOnlyAfterCoverage(t *testing.T) {
	f, c := newBscDemandTest(t)
	created := f.base.Add(100 * time.Second)
	seedBscDemandOrder(t, created, mdb.StatusExpired)
	end := created.Add(config.GetOrderExpirationTimeDuration() + bscOrderScanGrace)
	last := 2 * (end.Unix() - f.base.Unix()) // exact end timestamp still needs a batch
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, last); err != nil {
		t.Fatal(err)
	}
	var p bscBackfillProgress
	if _, err := bscDemandStep(t, c, &p, end.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(f.scans) != 1 {
		t.Fatalf("expired unscanned order omitted: %v", f.scans)
	}
	headers := f.headers
	if _, err := bscDemandStep(t, c, &p, end.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(f.scans) != 1 || f.headers != headers {
		t.Fatal("covered expired window still consumes RPC")
	}
}

func TestBscDemandIdleRestartOnlyReadsCursorHeaderOnce(t *testing.T) {
	f, c := newBscDemandTest(t)
	seedBscDemandOrder(t, f.base, mdb.StatusExpired)
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 8000); err != nil {
		t.Fatal(err)
	}
	var p bscBackfillProgress
	for i := 0; i < 5; i++ {
		if _, err := bscDemandStep(t, c, &p, f.base.Add(10*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if f.headers != 1 || len(f.scans) != 0 {
		t.Fatalf("idle restart calls %d, %v", f.headers, f.scans)
	}
}

func TestBscDemandDatabaseFailureDoesNotAdvance(t *testing.T) {
	f, c := newBscDemandTest(t)
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 100); err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Migrator().DropTable(&mdb.Orders{}); err != nil {
		t.Fatal(err)
	}
	var p bscBackfillProgress
	if _, err := bscDemandStep(t, c, &p, f.base.Add(time.Hour)); err == nil {
		t.Fatal("DB failure mistaken for idle")
	}
	row, _ := data.GetEvmScanCursor(mdb.NetworkBsc)
	if row.LastBlock != 100 || len(f.scans) != 0 {
		t.Fatal("advanced cursor despite failed demand read")
	}
}

func TestBscDemandMissingHeaderDoesNotAdvance(t *testing.T) {
	f, c := newBscDemandTest(t)
	created := f.base.Add(4500 * time.Second)
	seedBscDemandOrder(t, created, mdb.StatusWaitPay)
	f.failHeaderAt = 8769
	var p bscBackfillProgress
	if _, err := bscDemandStep(t, c, &p, created.Add(time.Minute)); err == nil {
		t.Fatal("missing header accepted")
	}
	row, _ := data.GetEvmScanCursor(mdb.NetworkBsc)
	if row.ID != 0 || len(f.scans) != 0 {
		t.Fatal("advanced without required header")
	}
}

func TestBscDemandCursorWriteFailureRetriesSameBatch(t *testing.T) {
	f, c := newBscDemandTest(t)
	created := f.base.Add(4500 * time.Second)
	seedBscDemandOrder(t, created, mdb.StatusWaitPay)
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 100); err != nil {
		t.Fatal(err)
	}
	// Inject a persistence failure only in the temporary test runtime DB.
	if err := dao.RuntimeDB.Exec("CREATE TRIGGER fail_cursor_write BEFORE INSERT ON evm_scan_cursor BEGIN SELECT RAISE(ABORT, 'test cursor write failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	var p bscBackfillProgress
	if _, err := bscDemandStep(t, c, &p, created.Add(time.Minute)); err == nil {
		t.Fatal("cursor persistence failure ignored")
	}
	row, err := data.GetEvmScanCursor(mdb.NetworkBsc)
	if err != nil || row.LastBlock != 100 || p.lastBlock != 100 {
		t.Fatalf("advanced without persisted coverage: %+v %+v %v", row, p, err)
	}
	if err := dao.RuntimeDB.Exec("DROP TRIGGER fail_cursor_write").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := bscDemandStep(t, c, &p, created.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(f.scans) != 2 || f.scans[0] != f.scans[1] {
		t.Fatalf("failed cursor write skipped a batch: %v", f.scans)
	}
}

func TestBscDemandRecoversExpiredPaymentAndDeduplicates(t *testing.T) {
	f, c := newBscDemandTest(t)
	created := f.base.Add(4000 * time.Second)
	o := seedBscDemandOrder(t, created, mdb.StatusExpired)
	paidBlock := int64(8100) // created+50s, inside original payment window
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 8099); err != nil {
		t.Fatal(err)
	}
	raw := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil).FillBytes(make([]byte, 32))
	f.logs = []types.Log{{Address: bscTestContract, Topics: []common.Hash{transferEventHash, {}, common.BytesToHash(bscTestRecipient.Bytes())}, Data: raw, BlockNumber: uint64(paidBlock), TxHash: common.HexToHash("0xabc"), BlockHash: common.HexToHash("0x123")}}
	var p bscBackfillProgress
	if _, err := bscDemandStep(t, c, &p, created.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	paid, err := data.GetOrderInfoByTradeId(o.TradeId)
	if err != nil || paid.Status != mdb.StatusPaySuccess || paid.BlockTransactionId != f.logs[0].TxHash.Hex() {
		t.Fatalf("not recovered: %+v %v", paid, err)
	}
	// Simulate replay after a crash between payment processing and cursor save.
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 8099); err != nil {
		t.Fatal(err)
	}
	p = bscBackfillProgress{}
	if _, err := bscDemandStep(t, c, &p, created.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	again, _ := data.GetOrderInfoByTradeId(o.TradeId)
	if again.BlockTransactionId != paid.BlockTransactionId || again.UpdatedAt.TimestampMilli() != paid.UpdatedAt.TimestampMilli() {
		t.Fatal("replayed payment modified paid order")
	}
}

func TestBscDemandRejectsPaymentAfterOriginalExpiration(t *testing.T) {
	f, c := newBscDemandTest(t)
	created := f.base.Add(4000 * time.Second)
	o := seedBscDemandOrder(t, created, mdb.StatusExpired)
	paidBlock := int64(9300) // created+650s, after the ten-minute payment window
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, paidBlock-1); err != nil {
		t.Fatal(err)
	}
	raw := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil).FillBytes(make([]byte, 32))
	f.logs = []types.Log{{Address: bscTestContract, Topics: []common.Hash{transferEventHash, {}, common.BytesToHash(bscTestRecipient.Bytes())}, Data: raw, BlockNumber: uint64(paidBlock), TxHash: common.HexToHash("0xdef"), BlockHash: common.HexToHash("0x123")}}
	var p bscBackfillProgress
	if _, err := bscDemandStep(t, c, &p, created.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	order, _ := data.GetOrderInfoByTradeId(o.TradeId)
	if order.Status != mdb.StatusExpired {
		t.Fatal("scan grace incorrectly extended payment deadline")
	}
}

func TestBscScheduledHealthIsSparseWhenIdle(t *testing.T) {
	f, _ := newBscDemandTest(t)
	now := f.base.Add(time.Hour)
	node := mdb.RpcNode{Network: mdb.NetworkBsc, Type: mdb.RpcNodeTypeHttp, LastCheckedAt: *carbon.NewTime(carbon.CreateFromStdTime(now.Add(-time.Minute)))}
	if scheduledRpcProbeDue(node, now) {
		t.Fatal("idle probe ran too frequently")
	}
	if !scheduledRpcProbeDue(node, now.Add(4*time.Minute)) {
		t.Fatal("idle capability probe not scheduled")
	}
	seedBscDemandOrder(t, now.Add(-time.Minute), mdb.StatusWaitPay)
	if !scheduledRpcProbeDue(node, now) {
		t.Fatal("active order did not restore normal probes")
	}
	node.Network = mdb.NetworkEthereum
	if !scheduledRpcProbeDue(node, now) {
		t.Fatal("changed other chain schedule")
	}
}

func TestBscBackfillWakeOnWebsocketRecovery(t *testing.T) {
	for len(bscBackfillWake) > 0 {
		<-bscBackfillWake
	}
	wakeBscBackfill(mdb.NetworkBsc)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !waitBscBackfill(ctx, time.Hour) {
		t.Fatal("WS recovery failed to wake idle scanner")
	}
	cancel()
	if waitBscBackfill(ctx, time.Hour) {
		t.Fatal("scanner ignored cancellation")
	}
}

func TestBscDemandNewOrderWakesIdleScanner(t *testing.T) {
	f, c := newBscDemandTest(t)
	var p bscBackfillProgress
	created := f.base.Add(4500 * time.Second)
	if _, err := bscDemandStep(t, c, &p, created); err != nil {
		t.Fatal(err)
	}
	if len(f.scans) != 0 {
		t.Fatal("scanned without demand")
	}
	seedBscDemandOrder(t, created, mdb.StatusWaitPay)
	if _, err := bscDemandStep(t, c, &p, created.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.scans) != 1 || f.scans[0][0] != 8760 {
		t.Fatalf("new order not covered: %v", f.scans)
	}
}

func TestBscDemandRespectsConfirmationsAndDetectsStaleNode(t *testing.T) {
	f, c := newBscDemandTest(t)
	if err := dao.Mdb.Model(&mdb.Chain{}).Where("network = ?", mdb.NetworkBsc).Update("min_confirmations", 12).Error; err != nil {
		t.Fatal(err)
	}
	seedBscDemandOrder(t, f.base.Add(4800*time.Second), mdb.StatusWaitPay)
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 9985); err != nil {
		t.Fatal(err)
	}
	var p bscBackfillProgress
	if _, err := bscDemandStep(t, c, &p, f.base.Add(5000*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.scans) != 1 || f.scans[0] != [2]int64{9986, 9989} {
		t.Fatalf("scanned unconfirmed blocks: %v", f.scans)
	}
	f.mu.Lock()
	f.head = 9900
	f.mu.Unlock()
	if _, err := bscDemandStep(t, c, &p, f.base.Add(5000*time.Second)); err == nil {
		t.Fatal("stale node accepted")
	}
	row, _ := data.GetEvmScanCursor(mdb.NetworkBsc)
	if row.LastBlock != 9989 {
		t.Fatal("stale node rewound cursor")
	}
}

func TestBscDemandKeepsOldUncoveredOrdersAndExcludesOtherPayments(t *testing.T) {
	f, _ := newBscDemandTest(t)
	expiration := config.GetOrderExpirationTimeDuration()
	old := seedBscDemandOrder(t, f.base, mdb.StatusExpired)
	paid := seedBscDemandOrder(t, f.base.Add(time.Second), mdb.StatusPaySuccess)
	pending := seedBscDemandOrder(t, f.base.Add(2*time.Second), mdb.StatusWaitPay)
	other := seedBscDemandOrder(t, f.base.Add(3*time.Second), mdb.StatusWaitPay)
	if err := dao.Mdb.Model(other).Update("network", mdb.NetworkTron).Error; err != nil {
		t.Fatal(err)
	}
	hosted := seedBscDemandOrder(t, f.base.Add(4*time.Second), mdb.StatusWaitPay)
	if err := dao.Mdb.Model(hosted).Update("pay_provider", mdb.PaymentProviderOkPay).Error; err != nil {
		t.Fatal(err)
	}
	seedBscDemandOrder(t, f.base.Add(5*time.Second), mdb.StatusWaitSelect)
	deleted := seedBscDemandOrder(t, f.base.Add(6*time.Second), mdb.StatusWaitPay)
	if err := dao.Mdb.Delete(deleted).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := data.ListEvmBackfillOrders(mdb.NetworkBsc, f.base.Add(time.Minute), expiration, bscOrderScanGrace)
	if err != nil || len(rows) != 3 {
		t.Fatalf("uncovered rows=%+v err=%v", rows, err)
	}
	ids := map[uint64]bool{}
	for _, o := range rows {
		ids[o.ID] = true
	}
	if !ids[old.ID] || !ids[paid.ID] || !ids[pending.ID] {
		t.Fatal("missing unscanned payment window")
	}
	rows, err = data.ListEvmBackfillOrders(mdb.NetworkBsc, f.base.Add(24*time.Hour), expiration, bscOrderScanGrace)
	if err != nil || len(rows) != 1 || rows[0].ID != pending.ID {
		t.Fatalf("old pending order omitted or covered history retained: %+v %v", rows, err)
	}
}

func TestBscDemandOrderWindowUsesInstantsAcrossTimezones(t *testing.T) {
	f, _ := newBscDemandTest(t)
	utc := seedBscDemandOrder(t, f.base, mdb.StatusExpired)
	sgt := seedBscDemandOrder(t, f.base.Add(time.Millisecond), mdb.StatusExpired)
	for id, timestamp := range map[uint64]string{
		utc.ID: "2026-09-01 00:00:00+00:00",
		sgt.ID: "2026-09-01 08:00:00+08:00",
	} {
		if err := dao.Mdb.Model(&mdb.Orders{}).Where("id = ?", id).Update("created_at", timestamp).Error; err != nil {
			t.Fatal(err)
		}
	}
	expiration := config.GetOrderExpirationTimeDuration()
	end := f.base.Add(expiration + bscOrderScanGrace)
	rows, err := data.ListEvmBackfillOrders(mdb.NetworkBsc, end, expiration, bscOrderScanGrace)
	if err != nil || len(rows) != 2 {
		t.Fatalf("equivalent timestamps lost a payment window: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row.CreatedAt.TimestampMilli() != f.base.UnixMilli() {
			t.Fatalf("timezone conversion changed order time: %+v", row.CreatedAt)
		}
	}
	rows, err = data.ListEvmBackfillOrders(mdb.NetworkBsc, end.Add(time.Second), expiration, bscOrderScanGrace)
	if err != nil || len(rows) != 0 {
		t.Fatalf("covered window retained due to timezone: %+v %v", rows, err)
	}
}

func TestBscDemandInvalidHeadersCannotSkipHistory(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []*types.Header{nil, {Number: big.NewInt(5)}, {Number: big.NewInt(6), Time: 10}, {Number: big.NewInt(-1), Time: 10}, {Number: big.NewInt(5), Time: 1 << 63}} {
		read := func(context.Context, *big.Int) (*types.Header, error) { return bad, nil }
		if _, err := checkedBscHeader(ctx, read, big.NewInt(5)); err == nil {
			t.Fatalf("invalid header accepted: %+v", bad)
		}
	}
}
