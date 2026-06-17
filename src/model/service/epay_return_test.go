package service

import (
	"net/url"
	"testing"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/sign"
)

func TestBuildPublicRedirectURLRewritesOnlyEpayOrders(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	epayOrder := &mdb.Orders{
		TradeId:     "trade_epay_redirect",
		PaymentType: mdb.PaymentTypeEpay,
		RedirectUrl: "https://merchant.example/return",
	}
	wantRedirect := config.GetAppUri() + "/pay/return/trade_epay_redirect"
	if got := buildPublicRedirectURL(epayOrder); got != wantRedirect {
		t.Fatalf("epay redirect_url = %q, want internal return route", got)
	}

	gmpayOrder := &mdb.Orders{
		TradeId:     "trade_gmpay_redirect",
		PaymentType: mdb.PaymentTypeGmpay,
		RedirectUrl: "https://merchant.example/return",
	}
	if got := buildPublicRedirectURL(gmpayOrder); got != "https://merchant.example/return" {
		t.Fatalf("gmpay redirect_url = %q, want merchant raw redirect_url", got)
	}

	if got := buildPublicRedirectURL(&mdb.Orders{TradeId: "trade_empty", PaymentType: mdb.PaymentTypeEpay}); got != "" {
		t.Fatalf("empty redirect_url = %q, want empty", got)
	}
}

func TestBuildEPayResultParamsMatchesLegacyFields(t *testing.T) {
	params, err := BuildEPayResultParams(&mdb.Orders{
		TradeId: "trade_epay_params",
		OrderId: "order_epay_params",
		Name:    "VIP",
		Amount:  1,
	}, &mdb.ApiKey{
		Pid:       "1001",
		SecretKey: "epay-secret",
	})
	if err != nil {
		t.Fatalf("BuildEPayResultParams(): %v", err)
	}

	expected := map[string]string{
		"pid":          "1001",
		"trade_no":     "trade_epay_params",
		"out_trade_no": "order_epay_params",
		"type":         "alipay",
		"name":         "VIP",
		"money":        "1.0000",
		"trade_status": "TRADE_SUCCESS",
		"sign_type":    "MD5",
	}
	for key, want := range expected {
		if got := params[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}

	signParams := map[string]interface{}{
		"pid":          params["pid"],
		"trade_no":     params["trade_no"],
		"out_trade_no": params["out_trade_no"],
		"type":         params["type"],
		"name":         params["name"],
		"money":        params["money"],
		"trade_status": params["trade_status"],
	}
	wantSign, err := sign.Get(signParams, "epay-secret")
	if err != nil {
		t.Fatalf("sign.Get(): %v", err)
	}
	if got := params["sign"]; got != wantSign {
		t.Fatalf("sign = %q, want %q", got, wantSign)
	}
}

func TestBuildEPayResultParamsRejectsNonNumericPid(t *testing.T) {
	_, err := BuildEPayResultParams(&mdb.Orders{
		TradeId: "trade_bad_pid",
		OrderId: "order_bad_pid",
		Name:    "VIP",
		Amount:  1,
	}, &mdb.ApiKey{
		Pid:       "not-a-number",
		SecretKey: "epay-secret",
	})
	if err != constant.EPayReturnSignatureErr {
		t.Fatalf("error = %v, want %v", err, constant.EPayReturnSignatureErr)
	}
}

func TestResolveOrderApiKeyRejectsUnavailableRows(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := ResolveOrderApiKey(&mdb.Orders{TradeId: "missing_api_key_id"}); err != constant.OrderApiKeyUnavailableErr {
		t.Fatalf("missing api_key_id error = %v, want %v", err, constant.OrderApiKeyUnavailableErr)
	}

	row := &mdb.ApiKey{
		Name:      "disabled",
		Pid:       "1201",
		SecretKey: "disabled-secret",
		Status:    mdb.ApiKeyStatusDisable,
	}
	if err := dao.Mdb.Create(row).Error; err != nil {
		t.Fatalf("create disabled api key: %v", err)
	}
	if _, err := ResolveOrderApiKey(&mdb.Orders{TradeId: "disabled_api_key", ApiKeyID: row.ID}); err != constant.OrderApiKeyUnavailableErr {
		t.Fatalf("disabled api key error = %v, want %v", err, constant.OrderApiKeyUnavailableErr)
	}
}

func TestAppendQueryParamsPreservesMerchantQuery(t *testing.T) {
	target, err := appendQueryParams("https://merchant.example/return?from=merchant", map[string]string{
		"pid":          "1001",
		"trade_no":     "trade_epay_redirect",
		"trade_status": "TRADE_SUCCESS",
	})
	if err != nil {
		t.Fatalf("appendQueryParams(): %v", err)
	}

	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("url.Parse(): %v", err)
	}
	query := parsed.Query()
	if got := query.Get("from"); got != "merchant" {
		t.Fatalf("from = %q, want merchant", got)
	}
	if got := query.Get("pid"); got != "1001" {
		t.Fatalf("pid = %q, want 1001", got)
	}
	if got := query.Get("trade_no"); got != "trade_epay_redirect" {
		t.Fatalf("trade_no = %q, want trade_epay_redirect", got)
	}
	if got := query.Get("trade_status"); got != "TRADE_SUCCESS" {
		t.Fatalf("trade_status = %q, want TRADE_SUCCESS", got)
	}
}
