package route

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/labstack/echo/v4"
)

func TestEPayOrderQueryCompatibility(t *testing.T) {
	e := setupTestEnv(t)
	var merchant mdb.ApiKey
	if err := dao.Mdb.Where("pid = ?", "1").First(&merchant).Error; err != nil {
		t.Fatal(err)
	}
	order := mdb.Orders{TradeId: "query-trade", OrderId: "query-merchant-order", ApiKeyID: merchant.ID,
		Amount: 12.3456, ActualAmount: 1.76, Currency: "CNY", PaymentType: mdb.PaymentTypeEpay, EpayType: "usdt.tron", Status: mdb.StatusWaitPay}
	if err := dao.Mdb.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{mdb.StatusWaitPay, mdb.StatusWaitSelect, mdb.StatusConfirming, mdb.StatusManualReview, mdb.StatusExpired, mdb.StatusPaySuccess} {
		if err := dao.Mdb.Model(&order).Update("status", status).Error; err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(fmt.Sprintf("%s/status%d", method, status), func(t *testing.T) {
				params := url.Values{"act": {"order"}, "pid": {"1"}, "key": {testAPIToken}, "out_trade_no": {order.OrderId}}
				path := "/payments/epay/v1/order/create-transaction/api.php"
				req := httptest.NewRequest(method, path+"?"+params.Encode(), strings.NewReader(params.Encode()))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
				req.RemoteAddr = "203.0.113.50:43120"
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				var result map[string]interface{}
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatalf("invalid JSON: %s", rec.Body.String())
				}
				if rec.Code != http.StatusOK || result["code"] != float64(1) {
					t.Fatalf("query failed: HTTP %d %s", rec.Code, rec.Body.String())
				}
				wantStatus := float64(0)
				if status == mdb.StatusPaySuccess {
					wantStatus = 1
				}
				if result["status"] != wantStatus || (result["trade_status"] == "TRADE_SUCCESS") != (wantStatus == 1) {
					t.Fatalf("wrong paid state: %v", result)
				}
				if result["trade_no"] != order.TradeId || result["out_trade_no"] != order.OrderId || result["money"] != "12.3456" || result["type"] != "usdt.tron" {
					t.Fatalf("wrong order identity or original amount: %v", result)
				}
				if rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("authenticated query must not be cached")
				}
			})
		}
	}
}

func TestEPayOrderQueryAuthorization(t *testing.T) {
	e := setupTestEnv(t)
	var merchant mdb.ApiKey
	if err := dao.Mdb.Where("pid = ?", "1").First(&merchant).Error; err != nil {
		t.Fatal(err)
	}
	orders := []mdb.Orders{
		{TradeId: "owned-trade", OrderId: "owned-order", ApiKeyID: merchant.ID, PaymentType: mdb.PaymentTypeEpay, Status: mdb.StatusPaySuccess, Amount: 10},
		{TradeId: "foreign-trade", OrderId: "foreign-order", ApiKeyID: merchant.ID + 100, PaymentType: mdb.PaymentTypeEpay, Status: mdb.StatusPaySuccess, Amount: 10},
		{TradeId: "gmpay-trade", OrderId: "gmpay-order", ApiKeyID: merchant.ID, PaymentType: mdb.PaymentTypeGmpay, Status: mdb.StatusPaySuccess, Amount: 10},
		{TradeId: "child-trade", OrderId: "child-order", ParentTradeId: "owned-trade", ApiKeyID: merchant.ID, PaymentType: mdb.PaymentTypeEpay, Status: mdb.StatusPaySuccess, Amount: 10},
	}
	if err := dao.Mdb.Create(&orders).Error; err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, field, value string
		success            bool
	}{
		{"by trade number", "trade_no", "owned-trade", true},
		{"wrong key", "key", "wrong", false},
		{"missing key", "key", "", false},
		{"unknown merchant", "pid", "missing", false},
		{"foreign merchant order", "out_trade_no", "foreign-order", false},
		{"foreign trade number", "trade_no", "foreign-trade", false},
		{"non epay order", "out_trade_no", "gmpay-order", false},
		{"internal child", "out_trade_no", "child-order", false},
		{"missing order", "out_trade_no", "missing", false},
		{"missing order identifier", "out_trade_no", "", false},
		{"conflicting identifiers", "trade_no", "foreign-trade", false},
		{"unsupported action", "act", "refund", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			params := url.Values{"act": {"order"}, "pid": {"1"}, "key": {testAPIToken}, "out_trade_no": {"owned-order"}}
			params.Set(tt.field, tt.value)
			if tt.name == "by trade number" || tt.name == "foreign trade number" {
				params.Del("out_trade_no")
			}
			req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/api.php?"+params.Encode(), nil)
			req.RemoteAddr = "203.0.113.50:43120"
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			var result map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("invalid JSON: %s", rec.Body.String())
			}
			if (result["code"] == float64(1)) != tt.success {
				t.Fatalf("unexpected query authorization: %s", rec.Body.String())
			}
			if !tt.success && result["trade_no"] != nil {
				t.Fatalf("rejected query leaked order: %v", result)
			}
		})
	}
	params := url.Values{"act": {"order"}, "pid": {"1"}, "key": {testAPIToken}, "out_trade_no": {"owned-order"}}
	if err := dao.Mdb.Model(&merchant).Update("ip_whitelist", "198.51.100.8").Error; err != nil {
		t.Fatal(err)
	}
	for _, peer := range []string{"203.0.113.50", "198.51.100.8"} {
		req := httptest.NewRequest(http.MethodPost, "/payments/epay/v1/order/create-transaction/api.php", strings.NewReader(params.Encode()))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
		req.Header.Set(echo.HeaderXForwardedFor, "198.51.100.8")
		req.RemoteAddr = peer + ":43120"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		var result map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if (result["code"] == float64(1)) != (peer == "198.51.100.8") {
			t.Fatalf("IP whitelist ignored for %s: %v", peer, result)
		}
	}
	if err := dao.Mdb.Model(&merchant).Update("status", mdb.ApiKeyStatusDisable).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/api.php?"+params.Encode(), nil)
	req.RemoteAddr = "198.51.100.8:43120"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["code"] == float64(1) {
		t.Fatal("disabled merchant was authorized")
	}
}
