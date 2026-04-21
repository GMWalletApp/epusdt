package mq

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/assimon/luuu/internal/testutil"
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
)

func TestProcessExpiredOrdersExpiresWaitingOrdersAndReleasesLocks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	order := &mdb.Orders{
		TradeId:        "trade_expired",
		OrderId:        "order_expired",
		Amount:         1,
		Currency:       "CNY",
		ActualAmount:   1,
		ReceiveAddress: "wallet_1",
		Token:          "USDT",
		Network:        "tron",
		Status:         mdb.StatusWaitPay,
		NotifyUrl:      "https://merchant.example/callback",
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create expired order: %v", err)
	}
	if err := dao.Mdb.Model(order).UpdateColumn("created_at", time.Now().Add(-20*time.Minute)).Error; err != nil {
		t.Fatalf("age expired order: %v", err)
	}
	if err := data.LockTransaction("tron", order.ReceiveAddress, order.Token, order.TradeId, order.ActualAmount, time.Hour); err != nil {
		t.Fatalf("lock expired order: %v", err)
	}

	recentOrder := &mdb.Orders{
		TradeId:        "trade_recent",
		OrderId:        "order_recent",
		Amount:         1,
		Currency:       "CNY",
		ActualAmount:   1.01,
		ReceiveAddress: "wallet_1",
		Token:          "USDT",
		Network:        "tron",
		Status:         mdb.StatusWaitPay,
		NotifyUrl:      "https://merchant.example/callback",
	}
	if err := dao.Mdb.Create(recentOrder).Error; err != nil {
		t.Fatalf("create recent order: %v", err)
	}
	if err := data.LockTransaction("tron", recentOrder.ReceiveAddress, recentOrder.Token, recentOrder.TradeId, recentOrder.ActualAmount, time.Hour); err != nil {
		t.Fatalf("lock recent order: %v", err)
	}

	processExpiredOrders()

	expired, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatalf("reload expired order: %v", err)
	}
	if expired.Status != mdb.StatusExpired {
		t.Fatalf("expired order status = %d, want %d", expired.Status, mdb.StatusExpired)
	}
	lockTradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken("tron", order.ReceiveAddress, order.Token, order.ActualAmount)
	if err != nil {
		t.Fatalf("expired order lock lookup: %v", err)
	}
	if lockTradeID != "" {
		t.Fatalf("expired order lock still exists: %s", lockTradeID)
	}

	recent, err := data.GetOrderInfoByTradeId(recentOrder.TradeId)
	if err != nil {
		t.Fatalf("reload recent order: %v", err)
	}
	if recent.Status != mdb.StatusWaitPay {
		t.Fatalf("recent order status = %d, want %d", recent.Status, mdb.StatusWaitPay)
	}
	lockTradeID, err = data.GetTradeIdByWalletAddressAndAmountAndToken("tron", recentOrder.ReceiveAddress, recentOrder.Token, recentOrder.ActualAmount)
	if err != nil {
		t.Fatalf("recent order lock lookup: %v", err)
	}
	if lockTradeID != recentOrder.TradeId {
		t.Fatalf("recent order lock = %s, want %s", lockTradeID, recentOrder.TradeId)
	}
}

func TestProcessExpiredOrdersKeepsPaidOrdersPaid(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	order := &mdb.Orders{
		TradeId:            "trade_paid",
		OrderId:            "order_paid",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_1",
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          "https://merchant.example/callback",
		BlockTransactionId: "block_paid",
		CallBackConfirm:    mdb.CallBackConfirmNo,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create paid order: %v", err)
	}
	if err := dao.Mdb.Model(order).UpdateColumn("created_at", time.Now().Add(-20*time.Minute)).Error; err != nil {
		t.Fatalf("age paid order: %v", err)
	}

	processExpiredOrders()

	current, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatalf("reload paid order: %v", err)
	}
	if current.Status != mdb.StatusPaySuccess {
		t.Fatalf("paid order status = %d, want %d", current.Status, mdb.StatusPaySuccess)
	}
	if current.BlockTransactionId != "block_paid" {
		t.Fatalf("paid order block transaction id = %s, want block_paid", current.BlockTransactionId)
	}
}

func TestDispatchPendingCallbacksHonorsBackoffAndPersistsSuccess(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	callbackLimiter = make(chan struct{}, 1)
	callbackInflight = sync.Map{}

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:            "trade_callback",
		OrderId:            "order_callback",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_1",
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          server.URL,
		BlockTransactionId: "block_callback",
		CallbackNum:        1,
		CallBackConfirm:    mdb.CallBackConfirmNo,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create callback order: %v", err)
	}

	dispatchPendingCallbacks()
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt32(&requestCount); got != 0 {
		t.Fatalf("unexpected callback count before backoff elapsed: %d", got)
	}

	if err := dao.Mdb.Model(order).UpdateColumn("updated_at", time.Now().Add(-2*time.Second)).Error; err != nil {
		t.Fatalf("age callback order: %v", err)
	}
	dispatchPendingCallbacks()

	waitFor(t, 3*time.Second, func() bool {
		current, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil || current.ID <= 0 {
			return false
		}
		return current.CallBackConfirm == mdb.CallBackConfirmOk && current.CallbackNum == 2
	})

	if got := atomic.LoadInt32(&requestCount); got != 1 {
		t.Fatalf("callback request count = %d, want 1", got)
	}
}

func TestSendOrderCallbackUsesActualPaidTokenAndNetwork(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	var callbackBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&callbackBody); err != nil {
			t.Fatalf("decode callback body: %v", err)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	parent := &mdb.Orders{
		TradeId:         "trade_parent_actual",
		OrderId:         "order_parent_actual",
		Amount:          1.24,
		Currency:        "CNY",
		ActualAmount:    0.18,
		ReceiveAddress:  "wallet_parent",
		Token:           "USDT",
		Network:         mdb.NetworkTron,
		Status:          mdb.StatusWaitPay,
		NotifyUrl:       server.URL,
		CallBackConfirm: mdb.CallBackConfirmNo,
	}
	if err := dao.Mdb.Create(parent).Error; err != nil {
		t.Fatalf("create parent order: %v", err)
	}
	sub := &mdb.Orders{
		TradeId:            "trade_sub_actual",
		OrderId:            "order_sub_actual",
		ParentTradeId:      parent.TradeId,
		Amount:             1.24,
		Currency:           "CNY",
		ActualAmount:       0.17,
		ReceiveAddress:     "0x08c34c4e8b99e2503017ae09287bd0019b7096c6",
		Token:              "USDC",
		Network:            mdb.NetworkEthereum,
		Status:             mdb.StatusPaySuccess,
		BlockTransactionId: "block_sub_actual",
		CallBackConfirm:    mdb.CallBackConfirmOk,
	}
	if err := dao.Mdb.Create(sub).Error; err != nil {
		t.Fatalf("create sub-order: %v", err)
	}
	if _, err := data.MarkParentOrderSuccess(parent.TradeId, sub); err != nil {
		t.Fatalf("mark parent success: %v", err)
	}

	parent, err := data.GetOrderInfoByTradeId(parent.TradeId)
	if err != nil {
		t.Fatalf("reload parent order: %v", err)
	}
	if err = sendOrderCallback(parent); err != nil {
		t.Fatalf("send callback: %v", err)
	}

	if callbackBody["token"] != "USDC" {
		t.Fatalf("expected callback token USDC, got %v", callbackBody["token"])
	}
	if callbackBody["network"] != mdb.NetworkEthereum {
		t.Fatalf("expected callback network %s, got %v", mdb.NetworkEthereum, callbackBody["network"])
	}
	if callbackBody["receive_address"] != sub.ReceiveAddress {
		t.Fatalf("expected callback receive address %s, got %v", sub.ReceiveAddress, callbackBody["receive_address"])
	}
}

func TestDispatchPendingCallbacksResumesRetryAfterRestart(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	callbackLimiter = make(chan struct{}, 1)
	callbackInflight = sync.Map{}

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&requestCount, 1)
		if attempt == 1 {
			http.Error(w, "retry later", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:            "trade_callback_restart",
		OrderId:            "order_callback_restart",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_restart",
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          server.URL,
		BlockTransactionId: "block_callback_restart",
		CallbackNum:        0,
		CallBackConfirm:    mdb.CallBackConfirmNo,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create callback order: %v", err)
	}

	dispatchPendingCallbacks()

	waitFor(t, 3*time.Second, func() bool {
		current, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil || current.ID <= 0 {
			return false
		}
		return current.CallBackConfirm == mdb.CallBackConfirmNo && current.CallbackNum == 1
	})

	if got := atomic.LoadInt32(&requestCount); got != 1 {
		t.Fatalf("first callback request count = %d, want 1", got)
	}

	callbackLimiter = make(chan struct{}, 1)
	callbackInflight = sync.Map{}

	if err := dao.Mdb.Model(order).UpdateColumn("updated_at", time.Now().Add(-2*time.Second)).Error; err != nil {
		t.Fatalf("age callback order for retry: %v", err)
	}

	dispatchPendingCallbacks()

	waitFor(t, 3*time.Second, func() bool {
		current, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil || current.ID <= 0 {
			return false
		}
		return current.CallBackConfirm == mdb.CallBackConfirmOk && current.CallbackNum == 2
	})

	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("total callback request count = %d, want 2", got)
	}
}

func TestDispatchPendingCallbacksEpayRequiresSuccessfulResponse(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	callbackLimiter = make(chan struct{}, 1)
	callbackInflight = sync.Map{}

	var callbackType atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		callbackType.Store(r.Form.Get("type"))
		_, _ = io.WriteString(w, "fail")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:         "trade_callback_epay_fail",
		OrderId:         "order_callback_epay_fail",
		Amount:          1,
		Currency:        "USD",
		ActualAmount:    1,
		ReceiveAddress:  "wallet_epay_fail",
		Token:           "USDT",
		Status:          mdb.StatusPaySuccess,
		NotifyUrl:       server.URL,
		CallbackNum:     0,
		CallBackConfirm: mdb.CallBackConfirmNo,
		PaymentType:     mdb.PaymentTypeEpay,
		PaymentChannel:  "wxpay",
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create epay callback order: %v", err)
	}

	dispatchPendingCallbacks()

	waitFor(t, 3*time.Second, func() bool {
		current, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil || current.ID <= 0 {
			return false
		}
		return current.CallBackConfirm == mdb.CallBackConfirmNo && current.CallbackNum == 1
	})

	if got, _ := callbackType.Load().(string); got != "wxpay" {
		t.Fatalf("callback type = %q, want wxpay", got)
	}
}

func TestDispatchPendingCallbacksEpayAcceptsSuccessResponse(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	callbackLimiter = make(chan struct{}, 1)
	callbackInflight = sync.Map{}

	var callbackType atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		callbackType.Store(r.Form.Get("type"))
		_, _ = io.WriteString(w, "success")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:         "trade_callback_epay_success",
		OrderId:         "order_callback_epay_success",
		Amount:          1,
		Currency:        "USD",
		ActualAmount:    1,
		ReceiveAddress:  "wallet_epay_success",
		Token:           "USDT",
		Status:          mdb.StatusPaySuccess,
		NotifyUrl:       server.URL,
		CallbackNum:     0,
		CallBackConfirm: mdb.CallBackConfirmNo,
		PaymentType:     mdb.PaymentTypeEpay,
		PaymentChannel:  "wxpay",
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create epay callback order: %v", err)
	}

	dispatchPendingCallbacks()

	waitFor(t, 3*time.Second, func() bool {
		current, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil || current.ID <= 0 {
			return false
		}
		return current.CallBackConfirm == mdb.CallBackConfirmOk && current.CallbackNum == 1
	})

	if got, _ := callbackType.Load().(string); got != "wxpay" {
		t.Fatalf("callback type = %q, want wxpay", got)
	}
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}
