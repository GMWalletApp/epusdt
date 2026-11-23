package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/http_client"
)

// The OKX signature vector was computed with the reference algorithm
// (Base64(HMAC-SHA256(secret, timestamp + method + path + body))):
// secret=testsecret, timestamp=2026-11-18T03:00:00.000Z,
// method=POST, path=/api/v6/pay/onchainos-payment/create,
// body={"merchantPaymentId":"test-001"}.
func TestXLayerSignMatchesOKXVector(t *testing.T) {
	got := xlayerSign("testsecret", "2026-11-18T03:00:00.000Z", "POST", "/api/v6/pay/onchainos-payment/create", `{"merchantPaymentId":"test-001"}`)
	want := "ZVOc33Ox1Iu6kIJa3np2imxTG78PFJYTgOZWNQagRm8="
	if got != want {
		t.Fatalf("xlayerSign = %s, want %s", got, want)
	}

	// Cross-check against a direct HMAC computation so a refactor of the
	// helper still has to match the raw algorithm.
	mac := hmac.New(sha256.New, []byte("testsecret"))
	_, _ = mac.Write([]byte("2026-11-18T03:00:00.000Z" + "POST" + "/api/v6/pay/onchainos-payment/create" + `{"merchantPaymentId":"test-001"}`))
	if direct := base64.StdEncoding.EncodeToString(mac.Sum(nil)); direct != want {
		t.Fatalf("direct HMAC = %s, want %s", direct, want)
	}
}

func TestNormalizeXLayerToken(t *testing.T) {
	cases := map[string]string{
		" usdc ": "USDC",
		"usd₮0":  "USDT0",
		"USDT0":  "USDT0",
		"usdg":   "USDG",
	}
	for in, want := range cases {
		if got := normalizeXLayerToken(in); got != want {
			t.Fatalf("normalizeXLayerToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestXLayerTokenContracts(t *testing.T) {
	for _, token := range []string{"USDC", "USDT0", "USDG"} {
		contract, ok := xlayerTokenContract(token)
		if !ok {
			t.Fatalf("no contract for %s", token)
		}
		if len(contract) != 42 || !strings.HasPrefix(contract, "0x") {
			t.Fatalf("bad contract for %s: %s", token, contract)
		}
	}
	if _, ok := xlayerTokenContract("USDT"); ok {
		t.Fatalf("plain USDT must not map to an X Layer contract")
	}
}

func seedXLayerRate(t *testing.T) {
	t.Helper()
	if err := data.SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "2", mdb.SettingTypeInt); err != nil {
		t.Fatalf("seed amount precision: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupRate, mdb.SettingKeyRateMode, config.RateModeFixed, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed rate mode: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupRate, mdb.SettingKeyRateForcedRateList, mdb.SettingDefaultRateForcedRateList, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed forced rate list: %v", err)
	}
}

func TestXLayerAmounts(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedXLayerRate(t)

	// 10 CNY at the default forced rate 1/6.8 = 1.470588... USDC.
	display, _, baseUnits, err := xlayerAmounts(10, "CNY")
	if err != nil {
		t.Fatalf("xlayerAmounts(10 CNY): %v", err)
	}
	if display != 1.47 {
		t.Fatalf("display amount = %v, want 1.47", display)
	}
	if baseUnits != "1470588" {
		t.Fatalf("base units = %s, want 1470588", baseUnits)
	}

	// 1 CNY is only ~0.15 USDC. The provider API enforces no minimum
	// amount (confirmed by the user 2026-11-18), so it must compute a
	// valid amount instead of being rejected locally.
	display, _, baseUnits, err = xlayerAmounts(1, "CNY")
	if err != nil {
		t.Fatalf("xlayerAmounts(1 CNY): %v", err)
	}
	if display != 0.15 {
		t.Fatalf("display amount = %v, want 0.15", display)
	}
	if baseUnits != "147058" {
		t.Fatalf("base units = %s, want 147058", baseUnits)
	}
}

// seedXLayerSettings seeds the xlayer settings group. The apiURL argument
// only fills the legacy xlayer.api_url row; since the 2026-11-21 4th
// review nothing reads it -- the base URL is the xlayerAPIBaseURL
// constant (see TestXLayerAPIURLSettingIsIgnored).
func seedXLayerSettings(t *testing.T, apiURL string) {
	t.Helper()
	settings := []struct {
		key   string
		value string
		typ   string
	}{
		{mdb.SettingKeyXLayerEnabled, "true", mdb.SettingTypeBool},
		{mdb.SettingKeyXLayerAPIKey, "test-api-key", mdb.SettingTypeString},
		{mdb.SettingKeyXLayerAPISecret, "test-api-secret", mdb.SettingTypeString},
		{mdb.SettingKeyXLayerPassphrase, "test-passphrase", mdb.SettingTypeString},
		{mdb.SettingKeyXLayerAPIURL, apiURL, mdb.SettingTypeString},
		{mdb.SettingKeyXLayerTimeoutSeconds, "5", mdb.SettingTypeInt},
		{mdb.SettingKeyXLayerAllowTokens, "USDC,USDT0,USDG", mdb.SettingTypeString},
		{mdb.SettingKeyXLayerToAddress, "0x68047f077ca2c71d6a6c03afaf87e438e07bdf84", mdb.SettingTypeString},
	}
	for _, s := range settings {
		if err := data.SetSetting(mdb.SettingGroupXLayer, s.key, s.value, s.typ); err != nil {
			t.Fatalf("seed xlayer setting %s: %v", s.key, err)
		}
	}
}

func TestCreateAndQueryXLayerOrderWithMockHTTP(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	// A real httptest server would be simpler, but the resty client factory
	// is the established seam in this repo; serve through it.
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		for _, header := range []string{"OK-ACCESS-KEY", "OK-ACCESS-SIGN", "OK-ACCESS-PASSPHRASE", "OK-ACCESS-TIMESTAMP"} {
			if r.Header.Get(header) == "" {
				t.Errorf("missing header %s", header)
			}
		}
		mac := hmac.New(sha256.New, []byte("test-api-secret"))
		_, _ = mac.Write([]byte(r.Header.Get("OK-ACCESS-TIMESTAMP") + "POST" + r.URL.Path + string(body)))
		if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); r.Header.Get("OK-ACCESS-SIGN") != want {
			t.Errorf("bad OK-ACCESS-SIGN for %s", r.URL.Path)
		}
		var payload string
		switch r.URL.Path {
		case "/api/v6/pay/onchainos-payment/create":
			payload = `{"code":0,"msg":"","data":{"merchantPaymentId":"test-order-1","paymentIntentId":"pi_test_1","status":"pending","to":"0x68047f077ca2c71d6a6c03afaf87e438e07bdf84","checkoutUrl":"https://web3.okx.com/pay/checkout/pi_test_1","expiresAt":"2026-11-18T03:30:00.000Z"}}`
		case "/api/v6/pay/onchainos-payment/query":
			payload = `{"code":0,"msg":"","data":{"merchantPaymentId":"test-order-1","paymentIntentId":"pi_test_1","status":"settled","to":"0x68047f077ca2c71d6a6c03afaf87e438e07bdf84","txHash":"0xabc123","fiatAmount":"10.00","fiatCurrency":"CNY","paymentToken":{"chainId":"196","tokenSymbol":"USDC","tokenAddress":"0xb6ceceab302e2e4948951ee7843fc24e92933061","tokenDecimal":6,"tokenAmount":"1470588"},"expiresAt":"2026-11-18T03:30:00.000Z"}}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"code":61009,"msg":"not found"}`)), Header: http.Header{}}, nil
		}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}
		resp.Request = r
		return resp, nil
	})
	oldFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		client := resty.NewWithClient(&http.Client{Transport: transport})
		client.SetTimeout(10 * time.Second)
		return client
	}
	t.Cleanup(func() { http_client.ClientFactory = oldFactory })

	seedXLayerSettings(t, "https://web3.okx.com")

	created, err := createXLayerOrder("test-order-1", "0x68047f077ca2c71d6a6c03afaf87e438e07bdf84", "10.00", "CNY", "USDC", "1470588", "https://example.com/pay/checkout-counter/test-order-1", "Test Cashier", "test", 600)
	if err != nil {
		t.Fatalf("createXLayerOrder: %v", err)
	}
	if created.PaymentIntentID != "pi_test_1" || created.CheckoutURL != "https://web3.okx.com/pay/checkout/pi_test_1" {
		t.Fatalf("unexpected create result: %+v", created)
	}

	queried, _, err := queryXLayerOrder("pi_test_1")
	if err != nil {
		t.Fatalf("queryXLayerOrder: %v", err)
	}
	if queried.Status != "settled" || queried.TxHash != "0xabc123" {
		t.Fatalf("unexpected query result: %+v", queried)
	}
	if queried.PaymentToken == nil || queried.PaymentToken.TokenAmount != "1470588" {
		t.Fatalf("unexpected payment token: %+v", queried.PaymentToken)
	}
}

func TestPollXLayerProviderOrdersSettlesOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedXLayerRate(t)

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		payload := `{"code":0,"msg":"","data":{"merchantPaymentId":"test-order-1","paymentIntentId":"pi_test_1","status":"settled","to":"0x68047f077ca2c71d6a6c03afaf87e438e07bdf84","txHash":"0xabc123","fiatAmount":"10.00","fiatCurrency":"CNY","paymentToken":{"chainId":"196","tokenSymbol":"USDC","tokenAddress":"0xb6ceceab302e2e4948951ee7843fc24e92933061","tokenDecimal":6,"tokenAmount":"1470588"},"expiresAt":"2026-11-18T03:30:00.000Z"}}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}
		resp.Request = r
		return resp, nil
	})
	oldFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		client := resty.NewWithClient(&http.Client{Transport: transport})
		client.SetTimeout(10 * time.Second)
		return client
	}
	t.Cleanup(func() { http_client.ClientFactory = oldFactory })

	seedXLayerSettings(t, "https://web3.okx.com")

	order := &mdb.Orders{
		TradeId:        "test-order-1",
		OrderId:        "test-order-1",
		Amount:         10,
		Currency:       "CNY",
		ActualAmount:   1.47,
		ReceiveAddress: "",
		Token:          "USDC",
		Network:        mdb.PaymentProviderXLayer,
		Status:         mdb.StatusWaitPay,
		PayProvider:    mdb.PaymentProviderXLayer,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	providerRow := &mdb.ProviderOrder{
		TradeId:         "test-order-1",
		Provider:        mdb.PaymentProviderXLayer,
		ProviderOrderID: "pi_test_1",
		ReceiveAddress:  data.GetXLayerToAddress(),
		PayURL:          "https://web3.okx.com/pay/checkout/pi_test_1",
		Amount:          1.470588,
		Coin:            "USDC",
		Status:          mdb.ProviderOrderStatusPending,
	}
	if err := dao.Mdb.Create(providerRow).Error; err != nil {
		t.Fatalf("seed provider order: %v", err)
	}

	if err := data.SetSetting(mdb.SettingGroupXLayer, mdb.SettingKeyXLayerToAddress, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", mdb.SettingTypeString); err != nil {
		t.Fatal(err)
	}
	PollXLayerProviderOrders()

	after, err := data.GetOrderInfoByTradeId("test-order-1")
	if err != nil {
		t.Fatalf("load order after poll: %v", err)
	}
	if after.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status = %d, want %d (paid)", after.Status, mdb.StatusPaySuccess)
	}
	afterRow, err := data.GetProviderOrderByTradeIDAndProvider("test-order-1", mdb.PaymentProviderXLayer)
	if err != nil {
		t.Fatalf("load provider order after poll: %v", err)
	}
	if afterRow.Status != mdb.ProviderOrderStatusPaid {
		t.Fatalf("provider order status = %s, want paid", afterRow.Status)
	}
}

func TestPollXLayerProviderOrdersReconcilesExpiredLocalOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedXLayerSettings(t, "https://web3.okx.com")
	seedXLayerRate(t)

	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		payload := `{"code":0,"msg":"","data":{"merchantPaymentId":"expired-local-xlayer","paymentIntentId":"pi_expired_local","status":"settled","to":"0x68047f077ca2c71d6a6c03afaf87e438e07bdf84","txHash":"0xsettled-after-expiry","fiatAmount":"10.00","fiatCurrency":"CNY","paymentToken":{"chainId":"196","tokenSymbol":"USDC","tokenAddress":"0xb6ceceab302e2e4948951ee7843fc24e92933061","tokenDecimal":6,"tokenAmount":"1470588"}}}`
		return &http.Response{StatusCode: http.StatusOK, Request: r, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})
	previous := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		return resty.NewWithClient(&http.Client{Transport: transport})
	}
	t.Cleanup(func() { http_client.ClientFactory = previous })

	order := &mdb.Orders{
		TradeId:      "expired-local-xlayer",
		OrderId:      "expired-local-xlayer",
		Amount:       10,
		Currency:     "CNY",
		ActualAmount: 1.47,
		Token:        "USDC",
		Network:      mdb.PaymentProviderXLayer,
		Status:       mdb.StatusExpired,
		PayProvider:  mdb.PaymentProviderXLayer,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.ProviderOrder{
		TradeId:         order.TradeId,
		Provider:        mdb.PaymentProviderXLayer,
		ProviderOrderID: "pi_expired_local",
		ReceiveAddress:  data.GetXLayerToAddress(),
		Amount:          1.470588,
		Coin:            "USDC",
		Status:          mdb.ProviderOrderStatusPending,
	}).Error; err != nil {
		t.Fatalf("seed provider order: %v", err)
	}

	PollXLayerProviderOrders()
	if requests != 1 {
		t.Fatalf("provider query count = %d, want 1", requests)
	}
	row, err := data.GetProviderOrderByTradeIDAndProvider(order.TradeId, mdb.PaymentProviderXLayer)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != mdb.ProviderOrderStatusPaid {
		t.Fatalf("provider status = %q, want paid", row.Status)
	}
	reloaded, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != mdb.StatusExpired {
		t.Fatalf("local status = %d, want expired", reloaded.Status)
	}
}

// TestXLayerAPIURLSettingIsIgnored pins the 2026-11-21 4th-review change:
// the Onchain OS Payment base URL is the fixed xlayerAPIBaseURL constant,
// so a hostile/stale xlayer.api_url row in the settings table must never
// redirect the signed requests elsewhere.
func TestXLayerAPIURLSettingIsIgnored(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedXLayerSettings(t, "https://attacker.example.com")

	var gotHost string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"","data":{}}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}
		resp.Request = r
		return resp, nil
	})
	oldFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		client := resty.NewWithClient(&http.Client{Transport: transport})
		client.SetTimeout(10 * time.Second)
		return client
	}
	t.Cleanup(func() { http_client.ClientFactory = oldFactory })

	if _, _, err := queryXLayerOrder("pi_probe"); err != nil {
		t.Fatalf("queryXLayerOrder: %v", err)
	}
	if gotHost != "web3.okx.com" {
		t.Fatalf("request host = %q, want web3.okx.com (xlayer.api_url must be ignored)", gotHost)
	}
}

func TestSwitchToXLayerFromCashierWithoutScanner(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	seedXLayerSettings(t, "")
	seedXLayerRate(t)
	parent := &mdb.Orders{TradeId: "cashier-xlayer", OrderId: "merchant-xlayer", Currency: "CNY", Amount: 10, Status: mdb.StatusWaitSelect}
	if err := dao.Mdb.Create(parent).Error; err != nil {
		t.Fatal(err)
	}
	req := &request.SwitchNetworkRequest{TradeId: parent.TradeId, Network: mdb.NetworkXLayer, Token: "USDT0"}
	set := func(key, value, kind string) {
		t.Helper()
		if err := data.SetSetting(mdb.SettingGroupXLayer, key, value, kind); err != nil {
			t.Fatal(err)
		}
	}
	set(mdb.SettingKeyXLayerEnabled, "false", mdb.SettingTypeBool)
	if _, err := SwitchNetwork(req); err != constant.PaymentProviderNotEnabled {
		t.Fatalf("disabled provider returned %v", err)
	}
	set(mdb.SettingKeyXLayerEnabled, "true", mdb.SettingTypeBool)
	set(mdb.SettingKeyXLayerAllowTokens, "USDC", mdb.SettingTypeString)
	if _, err := SwitchNetwork(req); err != constant.PaymentProviderNotSupport {
		t.Fatalf("disabled token returned %v", err)
	}
	set(mdb.SettingKeyXLayerAllowTokens, "USDT0", mdb.SettingTypeString)

	oldWatch := EnsurePaymentWatch
	EnsurePaymentWatch = func(string, string) error { t.Fatal("hosted checkout attempted a scanner subscription"); return nil }
	oldFactory := http_client.ClientFactory
	t.Cleanup(func() { EnsurePaymentWatch = oldWatch; http_client.ClientFactory = oldFactory })
	const checkoutURL = "https://web3.okx.com/pay/checkout/test"
	requests := 0
	http_client.ClientFactory = func() *resty.Client {
		return resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			if r.URL.Host != "web3.okx.com" || r.URL.Path != xlayerAPIPathCreate {
				t.Fatalf("unexpected provider request %s", r.URL)
			}
			return &http.Response{Request: r, StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"paymentIntentId":"test-intent","checkoutUrl":"` + checkoutURL + `"}}`))}, nil
		})})
	}
	for i := 0; i < 2; i++ {
		updated, err := SwitchNetwork(req)
		if err != nil {
			t.Fatal(err)
		}
		if updated.TradeId != parent.TradeId || updated.PaymentUrl != checkoutURL || updated.Token != "USDT0" || updated.Status != mdb.StatusWaitPay {
			t.Fatalf("unexpected cashier response %+v", updated)
		}
	}
	reloaded, err := GetCheckoutCounterByTradeId(parent.TradeId)
	if err != nil || reloaded.PaymentUrl != checkoutURL || requests != 1 {
		t.Fatalf("reload=%+v, create requests=%d, err=%v", reloaded, requests, err)
	}
}
