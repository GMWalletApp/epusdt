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

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/sign"
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

	placeholder := &mdb.Orders{
		TradeId:   "trade_wait_select_expired",
		OrderId:   "order_wait_select_expired",
		Amount:    1,
		Currency:  "CNY",
		Status:    mdb.StatusWaitSelect,
		NotifyUrl: "https://merchant.example/callback",
	}
	if err := dao.Mdb.Create(placeholder).Error; err != nil {
		t.Fatalf("create placeholder order: %v", err)
	}
	if err := dao.Mdb.Model(placeholder).UpdateColumn("created_at", time.Now().Add(-20*time.Minute)).Error; err != nil {
		t.Fatalf("age placeholder order: %v", err)
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

	expiredPlaceholder, err := data.GetOrderInfoByTradeId(placeholder.TradeId)
	if err != nil {
		t.Fatalf("reload expired placeholder: %v", err)
	}
	if expiredPlaceholder.Status != mdb.StatusExpired {
		t.Fatalf("placeholder status = %d, want %d", expiredPlaceholder.Status, mdb.StatusExpired)
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
		ApiKeyID:           1, // seeded by testutil.SetupTestDatabases
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
		ApiKeyID:           1, // seeded by testutil.SetupTestDatabases
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

func TestDispatchPendingCallbacksEpayRequiresAck(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	callbackLimiter = make(chan struct{}, 1)
	callbackInflight = sync.Map{}

	epayKey, err := data.GetEnabledApiKey("1001")
	if err != nil || epayKey == nil || epayKey.ID == 0 {
		t.Fatalf("load epay key: %v", err)
	}

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		http.Error(w, "fail", http.StatusInternalServerError)
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:            "trade_callback_epay_fail",
		OrderId:            "order_callback_epay_fail",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_epay_fail",
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          server.URL,
		BlockTransactionId: "block_callback_epay_fail",
		CallbackNum:        0,
		CallBackConfirm:    mdb.CallBackConfirmNo,
		PaymentType:        "epay",
		ApiKeyID:           epayKey.ID,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create callback order: %v", err)
	}

	dispatchPendingCallbacks()

	waitFor(t, 3*time.Second, func() bool {
		current, innerErr := data.GetOrderInfoByTradeId(order.TradeId)
		if innerErr != nil || current.ID <= 0 {
			return false
		}
		return current.CallBackConfirm == mdb.CallBackConfirmNo && current.CallbackNum == 1
	})

	if got := atomic.LoadInt32(&requestCount); got != 1 {
		t.Fatalf("callback request count = %d, want 1", got)
	}
}

func TestDispatchPendingCallbacksEpayAcceptsTrimmedOk(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	callbackLimiter = make(chan struct{}, 1)
	callbackInflight = sync.Map{}

	epayKey, err := data.GetEnabledApiKey("1001")
	if err != nil || epayKey == nil || epayKey.ID == 0 {
		t.Fatalf("load epay key: %v", err)
	}

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		_, _ = io.WriteString(w, " ok\n")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:            "trade_callback_epay_ok",
		OrderId:            "order_callback_epay_ok",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_epay_ok",
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          server.URL,
		BlockTransactionId: "block_callback_epay_ok",
		CallbackNum:        0,
		CallBackConfirm:    mdb.CallBackConfirmNo,
		PaymentType:        "epay",
		ApiKeyID:           epayKey.ID,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create callback order: %v", err)
	}

	dispatchPendingCallbacks()

	waitFor(t, 3*time.Second, func() bool {
		current, innerErr := data.GetOrderInfoByTradeId(order.TradeId)
		if innerErr != nil || current.ID <= 0 {
			return false
		}
		return current.CallBackConfirm == mdb.CallBackConfirmOk && current.CallbackNum == 1
	})

	if got := atomic.LoadInt32(&requestCount); got != 1 {
		t.Fatalf("callback request count = %d, want 1", got)
	}
}

// Orders must always carry an ApiKeyID (the default key is seeded at
// bootstrap and the signing middleware / EPAY inline flow always
// stamps it). If ApiKeyID is 0, resolveOrderApiKey returns an error —
// the callback won't sign with a guess.
func TestResolveOrderApiKeyRejectsZeroApiKeyID(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	order := &mdb.Orders{
		TradeId:  "no-api-key-id",
		ApiKeyID: 0,
	}
	if _, err := resolveOrderApiKey(order); err == nil {
		t.Fatal("expected error for ApiKeyID=0")
	}
}

// Happy path: when ApiKeyID points at an enabled row, resolveOrderApiKey
// returns it.
func TestResolveOrderApiKeyReturnsEnabledRow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	row := &mdb.ApiKey{
		Name:      "target",
		Pid:       "target-key",
		SecretKey: "target-secret",
		Status:    mdb.ApiKeyStatusEnable,
	}
	if err := dao.Mdb.Create(row).Error; err != nil {
		t.Fatalf("create api key: %v", err)
	}
	order := &mdb.Orders{
		TradeId:  "happy-path",
		ApiKeyID: row.ID,
	}
	got, err := resolveOrderApiKey(order)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil || got.ID != row.ID {
		t.Fatalf("resolved row = %+v, want id=%d", got, row.ID)
	}
}

func TestSendOrderCallbackGmpayUsesApiKeySecretByPid(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	key := &mdb.ApiKey{
		Name:      "gmpay-key",
		Pid:       "9001",
		SecretKey: "gmpay-secret-9001",
		Status:    mdb.ApiKeyStatusEnable,
	}
	if err := dao.Mdb.Create(key).Error; err != nil {
		t.Fatalf("create gmpay api key: %v", err)
	}

	warningKey := &mdb.ApiKey{
		Name:      "wrong-key",
		Pid:       "9002",
		SecretKey: "wrong-secret-9002",
		Status:    mdb.ApiKeyStatusEnable,
	}
	if err := dao.Mdb.Create(warningKey).Error; err != nil {
		t.Fatalf("create wrong api key: %v", err)
	}

	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:            "trade_gmpay_sign",
		OrderId:            "order_gmpay_sign",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_gmpay",
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          server.URL,
		BlockTransactionId: "block_gmpay_sign",
		ApiKeyID:           key.ID,
		PaymentType:        mdb.PaymentTypeGmpay,
	}

	if err := sendOrderCallback(order); err != nil {
		t.Fatalf("send callback: %v", err)
	}

	if received == nil {
		t.Fatal("expected callback payload")
	}
	if gotPid, _ := received["pid"].(string); gotPid != key.Pid {
		t.Fatalf("payload pid = %q, want %q", gotPid, key.Pid)
	}
	if _, ok := received["payment_type"]; ok {
		t.Fatal("GMPay callback payload must not include payment_type")
	}

	recvSig, _ := received["signature"].(string)
	if recvSig == "" {
		t.Fatal("payload signature is empty")
	}
	delete(received, "signature")

	calcSig, err := sign.GetHMACSHA256(received, key.SecretKey)
	if err != nil {
		t.Fatalf("calc signature with target secret: %v", err)
	}
	if recvSig != calcSig {
		t.Fatalf("signature mismatch: got %q want %q", recvSig, calcSig)
	}

	wrongSig, err := sign.GetHMACSHA256(received, warningKey.SecretKey)
	if err != nil {
		t.Fatalf("calc signature with wrong secret: %v", err)
	}
	if recvSig == wrongSig {
		t.Fatal("signature should not match wrong api key secret")
	}
}

func TestSendOrderCallbackEpayUsesApiKeySecretByPid(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	key := &mdb.ApiKey{
		Name:      "epay-key",
		Pid:       "9101",
		SecretKey: "epay-secret-9101",
		Status:    mdb.ApiKeyStatusEnable,
	}
	if err := dao.Mdb.Create(key).Error; err != nil {
		t.Fatalf("create epay api key: %v", err)
	}

	warningKey := &mdb.ApiKey{
		Name:      "wrong-epay-key",
		Pid:       "9102",
		SecretKey: "wrong-epay-secret-9102",
		Status:    mdb.ApiKeyStatusEnable,
	}
	if err := dao.Mdb.Create(warningKey).Error; err != nil {
		t.Fatalf("create wrong epay api key: %v", err)
	}

	formPayload := map[string]string{}
	callbackMethod := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbackMethod = r.Method
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for k, v := range r.Form {
			if len(v) > 0 {
				formPayload[k] = v[0]
			}
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:            "trade_epay_sign",
		OrderId:            "order_epay_sign",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_epay",
		Token:              "USDT",
		Name:               "VIP",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          server.URL,
		BlockTransactionId: "block_epay_sign",
		EpayType:           "usdt.tron",
		PaymentType:        "epay",
		ApiKeyID:           key.ID,
	}

	if err := sendOrderCallback(order); err != nil {
		t.Fatalf("send epay callback: %v", err)
	}

	if callbackMethod != http.MethodGet {
		t.Fatalf("callback method = %s, want %s", callbackMethod, http.MethodGet)
	}
	if formPayload["sign_type"] != "MD5" {
		t.Fatalf("sign_type = %q, want MD5", formPayload["sign_type"])
	}
	if formPayload["type"] != "usdt.tron" {
		t.Fatalf("type = %q, want usdt.tron", formPayload["type"])
	}
	if formPayload["trade_status"] != "TRADE_SUCCESS" {
		t.Fatalf("trade_status = %q, want TRADE_SUCCESS", formPayload["trade_status"])
	}
	if _, ok := formPayload["payment_type"]; ok {
		t.Fatal("EPay callback payload must not include payment_type")
	}

	if formPayload["pid"] != key.Pid {
		t.Fatalf("form pid = %q, want %q", formPayload["pid"], key.Pid)
	}
	recvSig := formPayload["sign"]
	if recvSig == "" {
		t.Fatal("epay sign is empty")
	}

	signParams := map[string]interface{}{
		"pid":          formPayload["pid"],
		"trade_no":     formPayload["trade_no"],
		"out_trade_no": formPayload["out_trade_no"],
		"type":         formPayload["type"],
		"name":         formPayload["name"],
		"money":        formPayload["money"],
		"trade_status": formPayload["trade_status"],
	}

	calcSig, err := sign.Get(signParams, key.SecretKey)
	if err != nil {
		t.Fatalf("calc epay signature with target secret: %v", err)
	}
	if recvSig != calcSig {
		t.Fatalf("epay signature mismatch: got %q want %q", recvSig, calcSig)
	}

	wrongSig, err := sign.Get(signParams, warningKey.SecretKey)
	if err != nil {
		t.Fatalf("calc epay signature with wrong secret: %v", err)
	}
	if recvSig == wrongSig {
		t.Fatal("epay signature should not match wrong api key secret")
	}
}

func TestSendOrderCallbackEpayPreservesStoredAlipayType(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	key := &mdb.ApiKey{
		Name:      "epay-key-stored-type",
		Pid:       "9201",
		SecretKey: "epay-secret-9201",
		Status:    mdb.ApiKeyStatusEnable,
	}
	if err := dao.Mdb.Create(key).Error; err != nil {
		t.Fatalf("create epay api key: %v", err)
	}

	formPayload := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for k, v := range r.Form {
			if len(v) > 0 {
				formPayload[k] = v[0]
			}
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:            "trade_epay_type_case_alipay",
		OrderId:            "order_epay_type_case_alipay",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_epay_type_case",
		Token:              "USDT",
		Name:               "VIP",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          server.URL,
		BlockTransactionId: "block_epay_type_case_alipay",
		EpayType:           "alipay",
		PaymentType:        "epay",
		ApiKeyID:           key.ID,
	}

	if err := sendOrderCallback(order); err != nil {
		t.Fatalf("send epay callback: %v", err)
	}
	if got := formPayload["type"]; got != "alipay" {
		t.Fatalf("type = %q, want alipay", got)
	}

	signParams := map[string]interface{}{
		"pid":          formPayload["pid"],
		"trade_no":     formPayload["trade_no"],
		"out_trade_no": formPayload["out_trade_no"],
		"type":         formPayload["type"],
		"name":         formPayload["name"],
		"money":        formPayload["money"],
		"trade_status": formPayload["trade_status"],
	}
	calcSig, err := sign.Get(signParams, key.SecretKey)
	if err != nil {
		t.Fatalf("calc epay signature: %v", err)
	}
	if got := formPayload["sign"]; got != calcSig {
		t.Fatalf("sign = %q, want %q", got, calcSig)
	}
}

func TestDispatchPendingCallbacksEpayAcceptsSuccessAck(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	callbackLimiter = make(chan struct{}, 1)
	callbackInflight = sync.Map{}

	epayKey, err := data.GetEnabledApiKey("1001")
	if err != nil || epayKey == nil || epayKey.ID == 0 {
		t.Fatalf("load epay key: %v", err)
	}

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		_, _ = io.WriteString(w, "success")
	}))
	defer server.Close()

	order := &mdb.Orders{
		TradeId:            "trade_callback_epay_success",
		OrderId:            "order_callback_epay_success",
		Amount:             1,
		Currency:           "CNY",
		ActualAmount:       1,
		ReceiveAddress:     "wallet_epay_success",
		Token:              "USDT",
		Status:             mdb.StatusPaySuccess,
		NotifyUrl:          server.URL,
		BlockTransactionId: "block_callback_epay_success",
		CallbackNum:        0,
		CallBackConfirm:    mdb.CallBackConfirmNo,
		PaymentType:        mdb.PaymentTypeEpay,
		ApiKeyID:           epayKey.ID,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create callback order: %v", err)
	}

	dispatchPendingCallbacks()

	waitFor(t, 3*time.Second, func() bool {
		current, innerErr := data.GetOrderInfoByTradeId(order.TradeId)
		if innerErr != nil || current.ID <= 0 {
			return false
		}
		return current.CallBackConfirm == mdb.CallBackConfirmOk && current.CallbackNum == 1
	})

	if got := atomic.LoadInt32(&requestCount); got != 1 {
		t.Fatalf("callback request count = %d, want 1", got)
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
