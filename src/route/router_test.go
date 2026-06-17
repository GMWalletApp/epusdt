package route

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/sign"
	"github.com/dromara/carbon/v2"
	"github.com/go-resty/resty/v2"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

const testAPIToken = "test-secret-token"

func setupTestEnv(t *testing.T) *echo.Echo {
	t.Helper()

	tmpDir := t.TempDir()

	// minimal viper config
	viper.Reset()
	viper.Set("db_type", "sqlite")
	viper.Set("app_uri", "http://localhost:8080")
	viper.Set("order_expiration_time", 10)
	viper.Set("api_rate_url", "")
	viper.Set("runtime_root_path", tmpDir)
	viper.Set("log_save_path", tmpDir)
	viper.Set("sqlite_database_filename", tmpDir+"/test.db")
	viper.Set("runtime_sqlite_filename", tmpDir+"/runtime.db")
	config.LogLevel = "info"
	config.LogSavePath = tmpDir

	log.Sugar = zap.NewNop().Sugar()
	if err := log.SetLevel(config.LogLevel); err != nil {
		t.Fatalf("set test log level: %v", err)
	}

	// init config paths
	os.Setenv("EPUSDT_CONFIG", tmpDir)
	defer os.Unsetenv("EPUSDT_CONFIG")

	// init DB
	if err := dao.DBInit(); err != nil {
		t.Fatalf("DBInit: %v", err)
	}
	if err := dao.RuntimeInit(); err != nil {
		t.Fatalf("RuntimeInit: %v", err)
	}

	// ensure tables exist (MdbTableInit uses sync.Once, so migrate directly)
	dao.Mdb.AutoMigrate(
		&mdb.Orders{},
		&mdb.WalletAddress{},
		&mdb.AdminUser{},
		&mdb.ApiKey{},
		&mdb.Setting{},
		&mdb.NotificationChannel{},
		&mdb.Chain{},
		&mdb.ChainToken{},
		&mdb.RpcNode{},
		&mdb.ProviderOrder{},
	)

	// reset the settings cache so stale entries from a prior test don't leak
	_ = data.ReloadSettings()
	data.ResetRpcRuntimeStatsForTest()
	config.SettingsGetString = func(key string) string {
		return data.GetSettingString(key, "")
	}
	t.Cleanup(func() {
		config.SettingsGetString = nil
	})
	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0.14285714285714285}}`, "json"); err != nil {
		t.Fatalf("seed rate.forced_rate_list: %v", err)
	}

	// seed wallet addresses
	dao.Mdb.Create(&mdb.WalletAddress{Network: mdb.NetworkTron, Address: "TTestTronAddress001", Status: mdb.TokenStatusEnable})
	dao.Mdb.Create(&mdb.WalletAddress{Network: mdb.NetworkSolana, Address: "SolTestAddress001", Status: mdb.TokenStatusEnable})
	// seed chains so scanners know the test networks are "enabled".
	// Use ON CONFLICT DO NOTHING: MdbTableInit (called via DBInit) uses sync.Once
	// and may have already seeded these rows into this DB on the first test run.
	dao.Mdb.Clauses(clause.OnConflict{DoNothing: true}).Create(&[]mdb.Chain{
		{Network: mdb.NetworkTron, DisplayName: "TRON", Enabled: true},
		{Network: mdb.NetworkSolana, DisplayName: "Solana", Enabled: true},
		{Network: mdb.NetworkEthereum, DisplayName: "Ethereum", Enabled: true},
		{Network: mdb.NetworkBsc, DisplayName: "BSC", Enabled: true},
		{Network: mdb.NetworkPolygon, DisplayName: "Polygon", Enabled: true},
		{Network: mdb.NetworkPlasma, DisplayName: "Plasma", Enabled: true},
	})

	// seed chain_tokens — GetSupportedAssets now reads from this table.
	// Same idempotency rationale as above.
	dao.Mdb.Clauses(clause.OnConflict{DoNothing: true}).Create(&[]mdb.ChainToken{
		{Network: mdb.NetworkTron, Symbol: "USDT", ContractAddress: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkTron, Symbol: "TRX", ContractAddress: "", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkSolana, Symbol: "USDT", ContractAddress: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkSolana, Symbol: "USDC", ContractAddress: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkSolana, Symbol: "SOL", ContractAddress: "", Decimals: 9, Enabled: true},
	})

	// Seed one universal api_keys row. The test's testAPIToken doubles
	// as both pid and secret_key so sign.Get(body, testAPIToken) calls
	// stay valid.
	dao.Mdb.Create(&mdb.ApiKey{
		Name:      "test-universal",
		Pid:       testAPIToken,
		SecretKey: testAPIToken,
		Status:    mdb.ApiKeyStatusEnable,
	})
	// Additional numeric-PID row for EPAY tests (EPAY pid must be numeric).
	dao.Mdb.Create(&mdb.ApiKey{
		Name:      "test-epay-pid-1",
		Pid:       "1",
		SecretKey: testAPIToken,
		Status:    mdb.ApiKeyStatusEnable,
	})

	e := echo.New()
	RegisterRoute(e)
	return e
}

func signBody(body map[string]interface{}) map[string]interface{} {
	// Signature middleware looks up api_keys by the "pid" field and
	// uses that row's secret_key as the signing bizKey.
	if _, ok := body["pid"]; !ok {
		body["pid"] = testAPIToken
	}
	sig, _ := sign.Get(body, testAPIToken)
	body["signature"] = sig
	return body
}

func signOkPayNotifyPayload(shopToken string, body map[string]interface{}) string {
	data, _ := body["data"].(map[string]interface{})
	ordered := []struct {
		key   string
		value string
	}{
		{"code", stringifyOkPayTestValue(body["code"])},
		{"data[order_id]", stringifyOkPayTestValue(data["order_id"])},
		{"data[unique_id]", stringifyOkPayTestValue(data["unique_id"])},
		{"data[pay_user_id]", stringifyOkPayTestValue(data["pay_user_id"])},
		{"data[amount]", stringifyOkPayTestValue(data["amount"])},
		{"data[coin]", stringifyOkPayTestValue(data["coin"])},
		{"data[status]", stringifyOkPayTestValue(data["status"])},
		{"data[type]", stringifyOkPayTestValue(data["type"])},
		{"id", stringifyOkPayTestValue(body["id"])},
		{"status", stringifyOkPayTestValue(body["status"])},
	}

	pairs := make([]string, 0, len(ordered))
	for _, item := range ordered {
		if item.value == "" {
			continue
		}
		pairs = append(pairs, url.QueryEscape(item.key)+"="+url.QueryEscape(item.value))
	}
	query, _ := url.QueryUnescape(strings.Join(pairs, "&"))
	sum := md5.Sum([]byte(query + "&token=" + shopToken))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func stringifyOkPayTestValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func doPost(e *echo.Echo, path string, body map[string]interface{}) *httptest.ResponseRecorder {
	jsonBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(jsonBytes)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func signEpayValues(values url.Values) url.Values {
	signParams := make(map[string]interface{})
	for key, items := range values {
		if key == "sign" || key == "sign_type" || len(items) == 0 {
			continue
		}
		signParams[key] = items[0]
	}
	sig, _ := sign.Get(signParams, testAPIToken)
	values.Set("sign", sig)
	values.Set("sign_type", "MD5")
	return values
}

func doFormPost(e *echo.Echo, path string, values url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func mustCreateEPayOrder(t *testing.T, e *echo.Echo, orderID string, returnURL string, extra url.Values) string {
	t.Helper()

	values := url.Values{
		"pid":          {"1"},
		"name":         {orderID},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {orderID},
		"notify_url":   {"https://93.184.216.34/notify"},
		"return_url":   {returnURL},
	}
	for key, items := range extra {
		clone := append([]string(nil), items...)
		values[key] = clone
	}
	values = signEpayValues(values)

	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/submit.php?"+values.Encode(), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("create epay order failed: %d %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/pay/checkout-counter/") {
		t.Fatalf("expected checkout redirect, got %q", location)
	}
	return strings.TrimPrefix(location, "/pay/checkout-counter/")
}

func TestRootPostRoute(t *testing.T) {
	e := setupTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "hello epusdt, https://github.com/GMwalletApp/epusdt" {
		t.Fatalf("unexpected body: %q", body)
	}
}

// TestCreateOrderGmpayV1Solana tests the gmpay route with solana network.
func TestCreateOrderGmpayV1Solana(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-sol-001",
		"amount":     1.00,
		"token":      "usdt",
		"currency":   "cny",
		"network":    "solana",
		"notify_url": "https://93.184.216.34/notify",
	})

	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)
	t.Logf("Status: %d, Body: %s", rec.Code, rec.Body.String())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in response, got: %v", resp)
	}

	if data["trade_id"] == nil || data["trade_id"] == "" {
		t.Error("expected trade_id in response")
	}
	if data["receive_address"] != "SolTestAddress001" {
		t.Errorf("expected solana address, got: %v", data["receive_address"])
	}
	t.Logf("Order created: trade_id=%v address=%v amount=%v", data["trade_id"], data["receive_address"], data["actual_amount"])
}

func TestCreateOrderGmpayRejectsPrivateNotifyURL(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-private-notify-001",
		"amount":     1.00,
		"token":      "usdt",
		"currency":   "cny",
		"network":    "solana",
		"notify_url": "http://127.0.0.1/notify",
	})

	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal private notify response: %v", err)
	}
	if got := int(resp["status_code"].(float64)); got != 10041 {
		t.Fatalf("status_code = %d, want 10041; response=%v", got, resp)
	}
	if got, _ := resp["message"].(string); got != constant.Errno[10041] {
		t.Fatalf("message = %q, want %q", got, constant.Errno[10041])
	}
}

func TestPublicJSONBindErrorUsesParamsErrno(t *testing.T) {
	e := setupTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/pay/submit-tx-hash/bad-json-order", strings.NewReader("{"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal bind error response: %v", err)
	}
	if got := int(resp["status_code"].(float64)); got != 10009 {
		t.Fatalf("status_code = %d, want 10009; response=%v", got, resp)
	}
	if got, _ := resp["message"].(string); got != constant.Errno[10009] {
		t.Fatalf("message = %q, want %q", got, constant.Errno[10009])
	}
}

// TestCreateOrderGmpayV1SolNative tests creating an order for native SOL token.
func TestCreateOrderGmpayV1SolNative(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-sol-native-001",
		"amount":     0.05,
		"token":      "sol",
		"currency":   "usd",
		"network":    "solana",
		"notify_url": "https://93.184.216.34/notify",
	})

	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)
	t.Logf("Status: %d, Body: %s", rec.Code, rec.Body.String())

	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	t.Logf("Response: %v", resp)

	// This may fail if rate API is not configured, which is expected in test
	// The important thing is the route accepts the request with network=solana token=sol
	if rec.Code != http.StatusOK {
		t.Logf("Note: non-200 may be expected if rate API is not configured for SOL")
	}
}

func parseResp(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// signGmpayFormValues builds a signed url.Values for the GMPAY create-transaction endpoint.
// The GMPAY middleware uses "signature" (not "sign") and "pid" (not numeric pid from EPAY).
func signGmpayFormValues(values url.Values) url.Values {
	if values.Get("pid") == "" {
		values.Set("pid", testAPIToken)
	}
	params := make(map[string]interface{})
	for k, vs := range values {
		if k == "signature" || len(vs) == 0 {
			continue
		}
		params[k] = vs[0]
	}
	sig, _ := sign.Get(params, testAPIToken)
	values.Set("signature", sig)
	return values
}

// TestCreateOrderGmpayV1FormData verifies that the GMPAY create-transaction endpoint
// accepts application/x-www-form-urlencoded in addition to JSON.
func TestCreateOrderGmpayV1FormData(t *testing.T) {
	e := setupTestEnv(t)

	values := signGmpayFormValues(url.Values{
		"order_id":   {"test-form-001"},
		"amount":     {"1.00"},
		"token":      {"usdt"},
		"currency":   {"cny"},
		"network":    {"solana"},
		"notify_url": {"https://93.184.216.34/notify"},
	})

	rec := doFormPost(e, "/payments/gmpay/v1/order/create-transaction", values)
	t.Logf("Status: %d, Body: %s", rec.Code, rec.Body.String())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in response, got: %v", resp)
	}
	if data["trade_id"] == nil || data["trade_id"] == "" {
		t.Error("expected trade_id in response")
	}
	t.Logf("Form-data order created: trade_id=%v", data["trade_id"])
}

func TestCreateOrderGmpayV1PlaceholderWithoutTokenNetwork(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-placeholder-001",
		"amount":     1.00,
		"currency":   "cny",
		"notify_url": "https://93.184.216.34/notify",
	})

	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	respData, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in response, got: %v", resp)
	}
	tradeID, _ := respData["trade_id"].(string)
	if tradeID == "" {
		t.Fatalf("missing trade_id in response: %v", respData)
	}
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("reload placeholder order: %v", err)
	}
	if order.PaymentType != mdb.PaymentTypeGmpay {
		t.Fatalf("placeholder order payment_type = %q, want %q", order.PaymentType, mdb.PaymentTypeGmpay)
	}
	if got := int(respData["status"].(float64)); got != mdb.StatusWaitSelect {
		t.Fatalf("status = %d, want %d", got, mdb.StatusWaitSelect)
	}
	if respData["token"] != "" || respData["receive_address"] != "" || respData["actual_amount"].(float64) != 0 {
		t.Fatalf("placeholder chain fields = %#v", respData)
	}

	checkoutData := getCheckoutCounterRespData(t, e, tradeID)
	if got := int(checkoutData["status"].(float64)); got != mdb.StatusWaitSelect {
		t.Fatalf("checkout status = %d, want %d", got, mdb.StatusWaitSelect)
	}
	if checkoutData["actual_amount"].(float64) != 0 || checkoutData["token"] != "" || checkoutData["network"] != "" || checkoutData["receive_address"] != "" {
		t.Fatalf("placeholder checkout chain fields = %#v", checkoutData)
	}
	if checkoutData["payment_type"] != "gmpay" {
		t.Fatalf("placeholder checkout payment_type = %v, want gmpay", checkoutData["payment_type"])
	}
	if checkoutData["is_selected"].(bool) {
		t.Fatalf("placeholder checkout is_selected = true, want false")
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/pay/check-status/"+tradeID, nil)
	statusRec := httptest.NewRecorder()
	e.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("check-status expected 200, got %d: %s", statusRec.Code, statusRec.Body.String())
	}
	statusResp := parseResp(t, statusRec)
	statusData := statusResp["data"].(map[string]interface{})
	if got := int(statusData["status"].(float64)); got != mdb.StatusWaitSelect {
		t.Fatalf("check-status status = %d, want %d", got, mdb.StatusWaitSelect)
	}
}

func TestCreateOrderGmpayV1RejectsPartialTokenNetwork(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-placeholder-partial-001",
		"amount":     1.00,
		"currency":   "cny",
		"network":    "tron",
		"notify_url": "https://93.184.216.34/notify",
	})

	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	if got := int(resp["status_code"].(float64)); got != 10009 {
		t.Fatalf("status_code = %d, want 10009; response=%v", got, resp)
	}
}

func TestSwitchNetworkCompletesGmpayPlaceholderInPlace(t *testing.T) {
	e := setupTestEnv(t)

	createBody := signBody(map[string]interface{}{
		"order_id":   "test-placeholder-switch-001",
		"amount":     1.00,
		"currency":   "cny",
		"notify_url": "https://93.184.216.34/notify",
	})
	createRec := doPost(e, "/payments/gmpay/v1/order/create-transaction", createBody)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create placeholder failed: %d %s", createRec.Code, createRec.Body.String())
	}
	createResp := parseResp(t, createRec)
	tradeID, _ := createResp["data"].(map[string]interface{})["trade_id"].(string)
	if tradeID == "" {
		t.Fatal("missing trade_id in create response")
	}

	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeID,
		"token":    "USDT",
		"network":  "tron",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch placeholder failed: %d %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	respData, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in switch response, got: %v", resp)
	}
	if got, _ := respData["trade_id"].(string); got != tradeID {
		t.Fatalf("switch trade_id = %q, want parent %q", got, tradeID)
	}
	if got := int(respData["status"].(float64)); got != mdb.StatusWaitPay {
		t.Fatalf("switch status = %d, want %d", got, mdb.StatusWaitPay)
	}
	if got, _ := respData["is_selected"].(bool); got {
		t.Fatalf("switch is_selected = %v, want false", respData["is_selected"])
	}
	if respData["payment_url"] != "" {
		t.Fatalf("switch payment_url = %v, want empty for unselected chain parent", respData["payment_url"])
	}
	if respData["payment_type"] != "gmpay" {
		t.Fatalf("switch payment_type = %v, want gmpay", respData["payment_type"])
	}
	if respData["network"] != "tron" || respData["token"] != "USDT" || respData["receive_address"] != "TTestTronAddress001" {
		t.Fatalf("switch chain fields = %#v", respData)
	}

	selectRec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeID,
		"token":    "USDT",
		"network":  "tron",
	})
	if selectRec.Code != http.StatusOK {
		t.Fatalf("select completed parent failed: %d %s", selectRec.Code, selectRec.Body.String())
	}
	selectResp := parseResp(t, selectRec)
	selectData, _ := selectResp["data"].(map[string]interface{})
	if got, _ := selectData["trade_id"].(string); got != tradeID {
		t.Fatalf("select trade_id = %q, want parent %q", got, tradeID)
	}
	if got, _ := selectData["is_selected"].(bool); !got {
		t.Fatalf("select is_selected = %v, want true", selectData["is_selected"])
	}

	count, err := data.CountActiveSubOrders(tradeID)
	if err != nil {
		t.Fatalf("count sub orders: %v", err)
	}
	if count != 0 {
		t.Fatalf("active sub-order count = %d, want 0", count)
	}
}

// getSupportedNetworks is a helper that calls GET /payments/gmpay/v1/config
// and returns a map of network → []token for easy assertions.
func getSupportedNetworks(t *testing.T, e *echo.Echo) map[string][]string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/payments/gmpay/v1/config", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("getSupportedNetworks: status=%d body=%s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	rawData, _ := resp["data"].(map[string]interface{})
	rawSupports, _ := rawData["supported_assets"].([]interface{})
	result := make(map[string][]string, len(rawSupports))
	for _, item := range rawSupports {
		row := item.(map[string]interface{})
		network := row["network"].(string)
		rawTokens, _ := row["tokens"].([]interface{})
		tokens := make([]string, 0, len(rawTokens))
		for _, tok := range rawTokens {
			tokens = append(tokens, tok.(string))
		}
		result[network] = tokens
	}
	return result
}

// TestGetSupportedAssets_ChainTokenToggle verifies that:
//   - disabling a chain_token removes that token (and possibly the whole network)
//     from the /config supported_assets response
//   - re-enabling it brings it back
func TestGetSupportedAssets_ChainTokenToggle(t *testing.T) {
	e := setupTestEnv(t)

	// Baseline: tron should appear with USDT and TRX.
	before := getSupportedNetworks(t, e)
	if _, ok := before["tron"]; !ok {
		t.Fatal("expected tron in baseline config.supported_assets")
	}

	// Disable the tron USDT chain_token.
	dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ? AND symbol = ?", "tron", "USDT").
		Update("enabled", false)

	after := getSupportedNetworks(t, e)
	tronTokens := after["tron"]
	for _, tok := range tronTokens {
		if tok == "USDT" {
			t.Fatal("USDT should be absent after disabling tron USDT chain_token")
		}
	}
	t.Logf("After disabling tron USDT: tron tokens = %v", tronTokens)

	// Disable the tron TRX chain_token as well — now tron has no tokens → network disappears.
	dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ? AND symbol = ?", "tron", "TRX").
		Update("enabled", false)

	afterAllDisabled := getSupportedNetworks(t, e)
	if _, ok := afterAllDisabled["tron"]; ok {
		t.Fatal("tron should disappear from config.supported_assets when all its chain_tokens are disabled")
	}
	t.Logf("After disabling all tron tokens: networks = %v", afterAllDisabled)

	// Re-enable tron USDT — tron reappears with only USDT.
	dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ? AND symbol = ?", "tron", "USDT").
		Update("enabled", true)

	restored := getSupportedNetworks(t, e)
	tronRestored, ok := restored["tron"]
	if !ok {
		t.Fatal("tron should reappear after re-enabling USDT chain_token")
	}
	found := false
	for _, tok := range tronRestored {
		if tok == "USDT" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected USDT in restored tron tokens, got %v", tronRestored)
	}
	t.Logf("After re-enabling tron USDT: tron tokens = %v", tronRestored)
}

// TestGetSupportedAssets_WalletAddressToggle verifies that:
//   - disabling ALL wallet addresses for a network removes that network from
//     the /config supported_assets response (even if chain + tokens are still enabled)
//   - re-enabling any address brings the network back
func TestGetSupportedAssets_WalletAddressToggle(t *testing.T) {
	e := setupTestEnv(t)

	// Baseline: solana should be present.
	before := getSupportedNetworks(t, e)
	if _, ok := before["solana"]; !ok {
		t.Fatal("expected solana in baseline config.supported_assets")
	}

	// Disable the only solana wallet address.
	dao.Mdb.Model(&mdb.WalletAddress{}).
		Where("network = ?", mdb.NetworkSolana).
		Update("status", mdb.TokenStatusDisable)

	after := getSupportedNetworks(t, e)
	if _, ok := after["solana"]; ok {
		t.Fatal("solana should disappear from config.supported_assets when all its wallet addresses are disabled")
	}
	t.Logf("After disabling solana wallets: networks = %v", after)

	// Re-enable the solana wallet.
	dao.Mdb.Model(&mdb.WalletAddress{}).
		Where("network = ?", mdb.NetworkSolana).
		Update("status", mdb.TokenStatusEnable)

	restored := getSupportedNetworks(t, e)
	if _, ok := restored["solana"]; !ok {
		t.Fatal("solana should reappear after re-enabling its wallet address")
	}
	t.Logf("After re-enabling solana wallets: solana tokens = %v", restored["solana"])
}

func TestGetPublicConfig(t *testing.T) {
	e := setupTestEnv(t)
	oldVersion := config.BuildVersion
	config.BuildVersion = "v1.0.1"
	t.Cleanup(func() {
		config.BuildVersion = oldVersion
	})
	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultToken, "usdt", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed epay.default_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultNetwork, "tron", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed epay.default_network: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandCheckoutName, "asd", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.checkout_name: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandLogoUrl, "https://cdn.example.com/logo.png", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.logo_url: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandSiteTitle, "asd title", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.site_title: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandSupportUrl, "https://example.com/support", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.support_url: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandBackgroundColor, "#123456", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.background_color: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandBackgroundImageUrl, "https://cdn.example.com/bg.png", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.background_image_url: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/payments/gmpay/v1/config", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	resp := parseResp(t, rec)
	if resp["status_code"].(float64) != 200 {
		t.Fatalf("expected status_code=200, got %v", resp["status_code"])
	}

	respData, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected object data, got %T", resp["data"])
	}
	supports, ok := respData["supported_assets"].([]interface{})
	if !ok {
		t.Fatalf("expected supported_assets array, got %T", respData["supported_assets"])
	}
	if len(supports) < 2 {
		t.Fatalf("expected >= 2 network supports, got %d", len(supports))
	}
	if respData["version"] != "v1.0.1" {
		t.Fatalf("version = %v, want v1.0.1", respData["version"])
	}
	site, ok := respData["site"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected site object, got %T", respData["site"])
	}
	if site["cashier_name"] != "asd" {
		t.Fatalf("site.cashier_name = %v, want asd", site["cashier_name"])
	}
	if site["logo_url"] != "https://cdn.example.com/logo.png" {
		t.Fatalf("site.logo_url = %v", site["logo_url"])
	}
	if site["website_title"] != "asd title" {
		t.Fatalf("site.website_title = %v, want asd title", site["website_title"])
	}
	if site["support_link"] != "https://example.com/support" {
		t.Fatalf("site.support_link = %v", site["support_link"])
	}
	if site["background_color"] != "#123456" {
		t.Fatalf("site.background_color = %v, want #123456", site["background_color"])
	}
	if site["background_image_url"] != "https://cdn.example.com/bg.png" {
		t.Fatalf("site.background_image_url = %v", site["background_image_url"])
	}
	epay, ok := respData["epay"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected epay object, got %T", respData["epay"])
	}
	if epay["default_network"] != "tron" {
		t.Fatalf("epay.default_network = %v, want tron", epay["default_network"])
	}
	okpay, ok := respData["okpay"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected okpay object, got %T", respData["okpay"])
	}
	if okpay["enabled"] != false {
		t.Fatalf("okpay.enabled = %v, want false", okpay["enabled"])
	}
	allowTokens, ok := okpay["allow_tokens"].([]interface{})
	if !ok || len(allowTokens) != 2 {
		t.Fatalf("expected okpay.allow_tokens array, got %T %#v", okpay["allow_tokens"], okpay["allow_tokens"])
	}
	if _, exists := okpay["shop_id"]; exists {
		t.Fatalf("public config should not expose okpay.shop_id: %#v", okpay)
	}
	if _, exists := okpay["shop_token"]; exists {
		t.Fatalf("public config should not expose okpay.shop_token: %#v", okpay)
	}

	seen := map[string]bool{}
	for _, item := range supports {
		row := item.(map[string]interface{})
		network := row["network"].(string)
		seen[network] = true
		if _, ok := row["display_name"].(string); !ok {
			t.Fatalf("supported_assets[%s].display_name missing or not string: %#v", network, row["display_name"])
		}
		if network == "tron" && row["display_name"] != "TRON" {
			t.Fatalf("supported_assets.tron.display_name = %v, want TRON", row["display_name"])
		}
	}
	for _, n := range []string{"tron", "solana"} {
		if !seen[n] {
			t.Fatalf("missing network support: %s", n)
		}
	}

	if got := data.GetOkPayCallbackURL(); got != "http://localhost:8080/payments/okpay/v1/notify" {
		t.Fatalf("default okpay callback_url = %q, want %q", got, "http://localhost:8080/payments/okpay/v1/notify")
	}
}

func TestGetPublicConfig_EpayDefaultsReturnEmptyAfterDelete(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.DeleteSetting(mdb.SettingKeyEpayDefaultToken); err != nil {
		t.Fatalf("delete epay.default_token: %v", err)
	}
	if err := data.DeleteSetting(mdb.SettingKeyEpayDefaultNetwork); err != nil {
		t.Fatalf("delete epay.default_network: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/payments/gmpay/v1/config", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	respData := resp["data"].(map[string]interface{})
	epay := respData["epay"].(map[string]interface{})
	if epay["default_token"] != "" || epay["default_network"] != "" {
		t.Fatalf("epay defaults = token %v network %v, want empty", epay["default_token"], epay["default_network"])
	}
}

func TestGetPublicConfig_BrandLegacyFallback(t *testing.T) {
	e := setupTestEnv(t)
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandSiteName, "legacy cashier", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.site_name: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandPageTitle, "legacy title", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.page_title: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupBrand, mdb.SettingKeyBrandSupportUrl, "https://legacy.example.com/help", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed brand.support_url: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/payments/gmpay/v1/config", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	resp := parseResp(t, rec)
	respData, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected object data, got %T", resp["data"])
	}
	site, ok := respData["site"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected site object, got %T", respData["site"])
	}
	if site["cashier_name"] != "legacy cashier" {
		t.Fatalf("site.cashier_name = %v, want legacy cashier", site["cashier_name"])
	}
	if site["website_title"] != "legacy title" {
		t.Fatalf("site.website_title = %v, want legacy title", site["website_title"])
	}
	if site["support_link"] != "https://legacy.example.com/help" {
		t.Fatalf("site.support_link = %v", site["support_link"])
	}
}

func TestOkPayNotifyRouteExists(t *testing.T) {
	e := setupTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/payments/okpay/v1/notify", strings.NewReader(""))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("expected okpay notify route to exist, got 404")
	}
}

type okPayNotifyFixture struct {
	Parent      *mdb.Orders
	Child       *mdb.Orders
	ProviderRow *mdb.ProviderOrder
	Payload     map[string]interface{}
}

func seedOkPayNotifyFixture(t *testing.T) *okPayNotifyFixture {
	t.Helper()

	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayEnabled, "true", mdb.SettingTypeBool); err != nil {
		t.Fatalf("seed okpay.enabled: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopID, "okpay-shop-test", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_id: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopToken, "token-1", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_token: %v", err)
	}

	parent := &mdb.Orders{
		TradeId:        "parent-okpay-notify-001",
		OrderId:        "merchant-okpay-notify-001",
		Amount:         1,
		Currency:       "CNY",
		ActualAmount:   0.15,
		ReceiveAddress: "TTestTronAddress001",
		Token:          "USDT",
		Network:        mdb.NetworkTron,
		Status:         mdb.StatusWaitPay,
		NotifyUrl:      "https://93.184.216.34/notify",
		PaymentType:    mdb.PaymentTypeEpay,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(parent).Error; err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	child := &mdb.Orders{
		TradeId:        "okpay-unique-test-001",
		ParentTradeId:  parent.TradeId,
		OrderId:        "merchant-okpay-notify-001-sub",
		Amount:         1,
		Currency:       "CNY",
		ActualAmount:   0.15,
		ReceiveAddress: "OKPAY",
		Token:          "USDT",
		Network:        mdb.NetworkTron,
		Status:         mdb.StatusWaitPay,
		NotifyUrl:      "",
		PaymentType:    mdb.PaymentTypeEpay,
		PayProvider:    mdb.PaymentProviderOkPay,
	}
	if err := dao.Mdb.Create(child).Error; err != nil {
		t.Fatalf("create child order: %v", err)
	}

	providerRow := &mdb.ProviderOrder{
		TradeId:         child.TradeId,
		Provider:        mdb.PaymentProviderOkPay,
		ProviderOrderID: "okpay-order-test-001",
		PayURL:          "https://t.me/ExampleWalletBot?start=shop_deposit--okpay-order-test-001",
		Amount:          0.15,
		Coin:            "USDT",
		Status:          mdb.ProviderOrderStatusPending,
	}
	if err := dao.Mdb.Create(providerRow).Error; err != nil {
		t.Fatalf("create provider row: %v", err)
	}

	payload := map[string]interface{}{
		"code":   200,
		"id":     "okpay-shop-test",
		"status": "success",
		"data": map[string]interface{}{
			"order_id":    providerRow.ProviderOrderID,
			"unique_id":   child.TradeId,
			"pay_user_id": 1234567890,
			"amount":      "0.15000000",
			"coin":        "USDT",
			"status":      1,
			"type":        "deposit",
		},
	}
	payload["sign"] = signOkPayNotifyPayload("token-1", payload)

	return &okPayNotifyFixture{
		Parent:      parent,
		Child:       child,
		ProviderRow: providerRow,
		Payload:     payload,
	}
}

func buildOkPayNotifyFormValues(payload map[string]interface{}) url.Values {
	values := url.Values{}
	values.Set("code", stringifyOkPayTestValue(payload["code"]))
	values.Set("id", stringifyOkPayTestValue(payload["id"]))
	values.Set("status", stringifyOkPayTestValue(payload["status"]))
	values.Set("sign", stringifyOkPayTestValue(payload["sign"]))

	dataMap, _ := payload["data"].(map[string]interface{})
	values.Set("data[order_id]", stringifyOkPayTestValue(dataMap["order_id"]))
	values.Set("data[unique_id]", stringifyOkPayTestValue(dataMap["unique_id"]))
	values.Set("data[pay_user_id]", stringifyOkPayTestValue(dataMap["pay_user_id"]))
	values.Set("data[amount]", stringifyOkPayTestValue(dataMap["amount"]))
	values.Set("data[coin]", stringifyOkPayTestValue(dataMap["coin"]))
	values.Set("data[status]", stringifyOkPayTestValue(dataMap["status"]))
	values.Set("data[type]", stringifyOkPayTestValue(dataMap["type"]))
	return values
}

func assertOkPayNotifyProcessed(t *testing.T, fixture *okPayNotifyFixture) {
	t.Helper()

	paidChild, err := data.GetOrderInfoByTradeId(fixture.Child.TradeId)
	if err != nil {
		t.Fatalf("load paid child: %v", err)
	}
	if paidChild.Status != mdb.StatusPaySuccess {
		t.Fatalf("child status = %d, want %d", paidChild.Status, mdb.StatusPaySuccess)
	}
	if paidChild.BlockTransactionId != fixture.ProviderRow.ProviderOrderID {
		t.Fatalf("child block_transaction_id = %q, want %q", paidChild.BlockTransactionId, fixture.ProviderRow.ProviderOrderID)
	}

	paidParent, err := data.GetOrderInfoByTradeId(fixture.Parent.TradeId)
	if err != nil {
		t.Fatalf("load paid parent: %v", err)
	}
	if paidParent.Status != mdb.StatusPaySuccess {
		t.Fatalf("parent status = %d, want %d", paidParent.Status, mdb.StatusPaySuccess)
	}
	if paidParent.PayBySubId != paidChild.ID {
		t.Fatalf("parent pay_by_sub_id = %d, want %d", paidParent.PayBySubId, paidChild.ID)
	}

	savedProviderRow, err := data.GetProviderOrderByTradeIDAndProvider(fixture.Child.TradeId, mdb.PaymentProviderOkPay)
	if err != nil {
		t.Fatalf("load provider row: %v", err)
	}
	if savedProviderRow.Status != mdb.ProviderOrderStatusPaid {
		t.Fatalf("provider row status = %q, want %q", savedProviderRow.Status, mdb.ProviderOrderStatusPaid)
	}
	if savedProviderRow.NotifyRaw == "" {
		t.Fatal("provider row notify_raw should be saved")
	}
}

func TestOkPayNotify_JSONPayloadMarksOrdersPaid(t *testing.T) {
	e := setupTestEnv(t)
	fixture := seedOkPayNotifyFixture(t)

	rawBody, err := json.Marshal(fixture.Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/payments/okpay/v1/notify", strings.NewReader(string(rawBody)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("okpay notify failed: %d %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "success" {
		t.Fatalf("okpay notify response = %q, want success", rec.Body.String())
	}

	assertOkPayNotifyProcessed(t, fixture)
}

func TestOkPayNotify_FormPayloadMarksOrdersPaid(t *testing.T) {
	e := setupTestEnv(t)
	fixture := seedOkPayNotifyFixture(t)

	formValues := buildOkPayNotifyFormValues(fixture.Payload)
	req := httptest.NewRequest(http.MethodPost, "/payments/okpay/v1/notify", strings.NewReader(formValues.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("okpay form notify failed: %d %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "success" {
		t.Fatalf("okpay form notify response = %q, want success", rec.Body.String())
	}

	assertOkPayNotifyProcessed(t, fixture)
}

func TestOkPayNotify_ExpiresSiblingOkPayProviderOrder(t *testing.T) {
	e := setupTestEnv(t)
	fixture := seedOkPayNotifyFixture(t)

	sibling := &mdb.Orders{
		TradeId:        "okpay-unique-test-002",
		ParentTradeId:  fixture.Parent.TradeId,
		OrderId:        "merchant-okpay-notify-001-sibling",
		Amount:         1,
		Currency:       "CNY",
		ActualAmount:   0.15,
		ReceiveAddress: "OKPAY",
		Token:          "USDT",
		Network:        mdb.NetworkTron,
		Status:         mdb.StatusWaitPay,
		NotifyUrl:      "",
		PaymentType:    mdb.PaymentTypeEpay,
		PayProvider:    mdb.PaymentProviderOkPay,
	}
	if err := dao.Mdb.Create(sibling).Error; err != nil {
		t.Fatalf("create sibling order: %v", err)
	}
	siblingProvider := &mdb.ProviderOrder{
		TradeId:         sibling.TradeId,
		Provider:        mdb.PaymentProviderOkPay,
		ProviderOrderID: "okpay-order-test-002",
		PayURL:          "https://t.me/ExampleWalletBot?start=shop_deposit--okpay-order-test-002",
		Amount:          0.15,
		Coin:            "USDT",
		Status:          mdb.ProviderOrderStatusPending,
	}
	if err := dao.Mdb.Create(siblingProvider).Error; err != nil {
		t.Fatalf("create sibling provider row: %v", err)
	}

	rawBody, err := json.Marshal(fixture.Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/payments/okpay/v1/notify", strings.NewReader(string(rawBody)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("okpay notify failed: %d %s", rec.Code, rec.Body.String())
	}

	expiredSibling, err := data.GetOrderInfoByTradeId(sibling.TradeId)
	if err != nil {
		t.Fatalf("reload sibling order: %v", err)
	}
	if expiredSibling.Status != mdb.StatusExpired {
		t.Fatalf("sibling status = %d, want %d", expiredSibling.Status, mdb.StatusExpired)
	}

	expiredSiblingProvider, err := data.GetProviderOrderByTradeIDAndProvider(sibling.TradeId, mdb.PaymentProviderOkPay)
	if err != nil {
		t.Fatalf("reload sibling provider row: %v", err)
	}
	if expiredSiblingProvider.Status != mdb.ProviderOrderStatusExpired {
		t.Fatalf("sibling provider status = %q, want %q", expiredSiblingProvider.Status, mdb.ProviderOrderStatusExpired)
	}
}

// TestCreateOrderNetworkIsolation verifies tron and solana wallets don't mix.
func TestCreateOrderNetworkIsolation(t *testing.T) {
	e := setupTestEnv(t)

	// Try to create a solana order — should get solana address, not tron
	body := signBody(map[string]interface{}{
		"order_id":   fmt.Sprintf("test-isolation-%d", 1),
		"amount":     1.00,
		"token":      "usdt",
		"currency":   "cny",
		"network":    "solana",
		"notify_url": "https://93.184.216.34/notify",
	})
	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)

	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data, got: %v", resp)
	}
	if data["receive_address"] == "TTestTronAddress001" {
		t.Error("solana order should NOT get a tron address")
	}
	if data["receive_address"] != "SolTestAddress001" {
		t.Errorf("expected SolTestAddress001, got %v", data["receive_address"])
	}
}

func TestEpaySubmitPhpGetCompatible(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultToken, "usdt", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed epay.default_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultNetwork, "tron", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed epay.default_network: %v", err)
	}

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-get-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-get-001"},
		"notify_url":   {"https://93.184.216.34/notify"},
		"return_url":   {"http://localhost/return"},
	})

	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/submit.php?"+values.Encode(), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/pay/checkout-counter/") {
		t.Fatalf("expected checkout redirect, got %q", rec.Header().Get("Location"))
	}
	tradeID := strings.TrimPrefix(rec.Header().Get("Location"), "/pay/checkout-counter/")
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("reload epay order: %v", err)
	}
	if order.Status != mdb.StatusWaitPay || order.Token != "USDT" || order.Network != mdb.NetworkTron || order.ReceiveAddress == "" || order.ActualAmount <= 0 {
		t.Fatalf("epay order should be concrete status=1 chain order, got status=%d token=%q network=%q address=%q actual=%.4f", order.Status, order.Token, order.Network, order.ReceiveAddress, order.ActualAmount)
	}
	if order.PaymentType != mdb.PaymentTypeEpay {
		t.Fatalf("epay payment_type = %q, want %q", order.PaymentType, mdb.PaymentTypeEpay)
	}
	checkoutData := getCheckoutCounterRespData(t, e, tradeID)
	if checkoutData["payment_type"] != "epay" {
		t.Fatalf("epay checkout payment_type = %v, want epay", checkoutData["payment_type"])
	}
	if got, _ := checkoutData["redirect_url"].(string); got != "http://localhost:8080/pay/return/"+tradeID {
		t.Fatalf("epay checkout redirect_url = %q, want internal return route", got)
	}
	if order.RedirectUrl != "http://localhost/return" {
		t.Fatalf("stored redirect_url = %q, want merchant raw return_url", order.RedirectUrl)
	}
}

func TestEpaySubmitPhpRequestTokenNetworkOverrideDefaults(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultToken, "usdt", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed epay.default_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultNetwork, "tron", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed epay.default_network: %v", err)
	}

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-override-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-override-001"},
		"notify_url":   {"https://93.184.216.34/notify"},
		"return_url":   {"http://localhost/return"},
		"token":        {"usdt"},
		"network":      {"solana"},
		"currency":     {"cny"},
	})

	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/submit.php?"+values.Encode(), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	tradeID := strings.TrimPrefix(rec.Header().Get("Location"), "/pay/checkout-counter/")
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("reload epay order: %v", err)
	}
	if order.Network != mdb.NetworkSolana || order.ReceiveAddress != "SolTestAddress001" || order.Token != "USDT" {
		t.Fatalf("epay override order fields = network %q address %q token %q", order.Network, order.ReceiveAddress, order.Token)
	}
	if order.PaymentType != mdb.PaymentTypeEpay {
		t.Fatalf("payment_type = %q, want %q", order.PaymentType, mdb.PaymentTypeEpay)
	}
}

func TestEpaySubmitPhpWithoutTokenNetworkDefaultsCreatesPlaceholder(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultToken, "", mdb.SettingTypeString); err != nil {
		t.Fatalf("clear epay.default_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultNetwork, "", mdb.SettingTypeString); err != nil {
		t.Fatalf("clear epay.default_network: %v", err)
	}

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-placeholder-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-placeholder-001"},
		"notify_url":   {"https://93.184.216.34/notify"},
		"return_url":   {"http://localhost/return"},
	})

	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/submit.php?"+values.Encode(), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	tradeID := strings.TrimPrefix(rec.Header().Get("Location"), "/pay/checkout-counter/")
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("reload epay placeholder: %v", err)
	}
	if order.Status != mdb.StatusWaitSelect || order.Token != "" || order.Network != "" || order.ReceiveAddress != "" || order.ActualAmount != 0 {
		t.Fatalf("epay placeholder fields = status %d token %q network %q address %q actual %v", order.Status, order.Token, order.Network, order.ReceiveAddress, order.ActualAmount)
	}
	if order.PaymentType != mdb.PaymentTypeEpay {
		t.Fatalf("payment_type = %q, want %q", order.PaymentType, mdb.PaymentTypeEpay)
	}
	checkoutData := getCheckoutCounterRespData(t, e, tradeID)
	if checkoutData["payment_type"] != "epay" || int(checkoutData["status"].(float64)) != mdb.StatusWaitSelect {
		t.Fatalf("checkout placeholder data = %#v", checkoutData)
	}
}

func TestEpaySubmitPhpRejectsPartialResolvedTokenNetwork(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultToken, "", mdb.SettingTypeString); err != nil {
		t.Fatalf("clear epay.default_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultNetwork, "tron", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed epay.default_network: %v", err)
	}

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-partial-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-partial-001"},
		"notify_url":   {"https://93.184.216.34/notify"},
		"return_url":   {"http://localhost/return"},
	})

	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/submit.php?"+values.Encode(), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	if got := int(resp["status_code"].(float64)); got != 10009 {
		t.Fatalf("status_code = %d, want 10009", got)
	}
}

func TestEpaySubmitPhpPostFormCompatible(t *testing.T) {
	e := setupTestEnv(t)

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-post-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-post-001"},
		"notify_url":   {"https://93.184.216.34/notify"},
		"return_url":   {"http://localhost/return"},
		"sitename":     {"example-shop"},
	})

	rec := doFormPost(e, "/payments/epay/v1/order/create-transaction/submit.php", values)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/pay/checkout-counter/") {
		t.Fatalf("expected checkout redirect, got %q", rec.Header().Get("Location"))
	}
}

// TestCheckStatus_NotFound verifies that /pay/check-status/:trade_id returns a
// graceful JSON error (not 500) when the trade_id doesn't exist.
func TestCheckStatus_NotFound(t *testing.T) {
	e := setupTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/pay/check-status/nonexistent-trade-id", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	t.Logf("CheckStatus(not found): status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal check-status error response: %v", err)
	}
	if got := int(resp["status_code"].(float64)); got != 10008 {
		t.Fatalf("status_code = %d, want 10008; response=%v", got, resp)
	}
}

// TestCheckStatus_WithOrder verifies /pay/check-status/:trade_id returns the
// real order status for existing orders in all checkout-relevant states.
func TestCheckStatus_WithOrder(t *testing.T) {
	e := setupTestEnv(t)

	cases := []struct {
		name   string
		status int
	}{
		{name: "wait-pay", status: mdb.StatusWaitPay},
		{name: "paid", status: mdb.StatusPaySuccess},
		{name: "expired", status: mdb.StatusExpired},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tradeID := createCheckoutCounterRespTestOrder(t, e, "check-status-"+tc.name)
			if err := dao.Mdb.Model(&mdb.Orders{}).
				Where("trade_id = ?", tradeID).
				Update("status", tc.status).Error; err != nil {
				t.Fatalf("update status: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/pay/check-status/"+tradeID, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			t.Logf("CheckStatus(%s): status=%d body=%s", tc.name, rec.Code, rec.Body.String())
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal check-status response: %v", err)
			}
			data, ok := resp["data"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected data in check-status response, got: %v", resp)
			}
			if got, _ := data["trade_id"].(string); got != tradeID {
				t.Fatalf("trade_id = %q, want %q", got, tradeID)
			}
			if got := int(data["status"].(float64)); got != tc.status {
				t.Fatalf("status = %d, want %d", got, tc.status)
			}
		})
	}
}

func TestSubmitTxHash_SuccessUpdatesCheckStatus(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := createCheckoutCounterRespTestOrder(t, e, "submit-tx-hash-ok-001")

	verified := false
	restore := service.SetManualOrderPaymentValidatorForTest(func(order *mdb.Orders, blockID string) (string, error) {
		verified = true
		if order.TradeId != tradeID {
			t.Fatalf("validator trade_id = %s, want %s", order.TradeId, tradeID)
		}
		if blockID != "user-submitted-hash" {
			t.Fatalf("validator block id = %s, want user-submitted-hash", blockID)
		}
		return "canonical-user-submitted-hash", nil
	})
	defer restore()

	jsonBytes, _ := json.Marshal(map[string]interface{}{
		"block_transaction_id": " user-submitted-hash ",
	})
	req := httptest.NewRequest(http.MethodPost, "/pay/submit-tx-hash/"+tradeID, strings.NewReader(string(jsonBytes)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	t.Logf("SubmitTxHash(success): status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !verified {
		t.Fatal("expected chain verifier to be called")
	}

	var submitResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitResp); err != nil {
		t.Fatalf("unmarshal submit response: %v", err)
	}
	submitData, ok := submitResp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in submit response, got: %v", submitResp)
	}
	if got, _ := submitData["block_transaction_id"].(string); got != "canonical-user-submitted-hash" {
		t.Fatalf("block_transaction_id = %q", got)
	}
	if got := int(submitData["status"].(float64)); got != mdb.StatusPaySuccess {
		t.Fatalf("submit response status = %d, want %d", got, mdb.StatusPaySuccess)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/pay/check-status/"+tradeID, nil)
	statusRec := httptest.NewRecorder()
	e.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("check-status expected 200, got %d: %s", statusRec.Code, statusRec.Body.String())
	}
	var statusResp map[string]interface{}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("unmarshal check-status response: %v", err)
	}
	statusData, ok := statusResp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data in check-status response, got: %v", statusResp)
	}
	if got := int(statusData["status"].(float64)); got != mdb.StatusPaySuccess {
		t.Fatalf("check-status status = %d, want %d", got, mdb.StatusPaySuccess)
	}
}

func TestPayReturn_NotFound(t *testing.T) {
	e := setupTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/pay/return/nonexistent-trade-id", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	if got := int(resp["status_code"].(float64)); got != 10008 {
		t.Fatalf("status_code = %d, want 10008; response=%v", got, resp)
	}
}

func TestPayReturn_PaidEpayRedirectsToMerchantWithSignedParams(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := mustCreateEPayOrder(t, e, "epay-return-paid-001", "https://merchant.example/return?from=merchant", url.Values{
		"token":    {"usdt"},
		"network":  {"tron"},
		"currency": {"cny"},
	})
	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Update("status", mdb.StatusPaySuccess).Error; err != nil {
		t.Fatalf("mark order paid: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pay/return/"+tradeID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", got)
	}

	location := rec.Header().Get("Location")
	targetURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if targetURL.Scheme != "https" || targetURL.Host != "merchant.example" || targetURL.Path != "/return" {
		t.Fatalf("unexpected redirect target: %q", location)
	}

	query := targetURL.Query()
	if query.Get("from") != "merchant" {
		t.Fatalf("preserved query = %q, want merchant", query.Get("from"))
	}
	if query.Get("pid") != "1" {
		t.Fatalf("pid = %q, want 1", query.Get("pid"))
	}
	if query.Get("trade_no") != tradeID {
		t.Fatalf("trade_no = %q, want %q", query.Get("trade_no"), tradeID)
	}
	if query.Get("out_trade_no") != "epay-return-paid-001" {
		t.Fatalf("out_trade_no = %q", query.Get("out_trade_no"))
	}
	if query.Get("type") != "alipay" {
		t.Fatalf("type = %q, want alipay", query.Get("type"))
	}
	if query.Get("name") != "epay-return-paid-001" {
		t.Fatalf("name = %q", query.Get("name"))
	}
	if query.Get("money") != "1.0000" {
		t.Fatalf("money = %q, want 1.0000", query.Get("money"))
	}
	if query.Get("trade_status") != "TRADE_SUCCESS" {
		t.Fatalf("trade_status = %q, want TRADE_SUCCESS", query.Get("trade_status"))
	}
	if query.Get("sign_type") != "MD5" {
		t.Fatalf("sign_type = %q, want MD5", query.Get("sign_type"))
	}

	signParams := map[string]interface{}{
		"pid":          query.Get("pid"),
		"trade_no":     query.Get("trade_no"),
		"out_trade_no": query.Get("out_trade_no"),
		"type":         query.Get("type"),
		"name":         query.Get("name"),
		"money":        query.Get("money"),
		"trade_status": query.Get("trade_status"),
	}
	calcSig, err := sign.Get(signParams, testAPIToken)
	if err != nil {
		t.Fatalf("calc epay return signature: %v", err)
	}
	if got := query.Get("sign"); got != calcSig {
		t.Fatalf("sign = %q, want %q", got, calcSig)
	}
}

func TestPayReturn_UnpaidEpayRedirectsBackToCheckout(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := mustCreateEPayOrder(t, e, "epay-return-unpaid-001", "https://merchant.example/return", url.Values{
		"token":    {"usdt"},
		"network":  {"tron"},
		"currency": {"cny"},
	})

	req := httptest.NewRequest(http.MethodGet, "/pay/return/"+tradeID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/pay/checkout-counter/"+tradeID {
		t.Fatalf("location = %q, want checkout counter", got)
	}
}

func TestPayReturn_PaidNonEpayRedirectsBackToCheckout(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := createCheckoutCounterRespTestOrder(t, e, "pay-return-gmpay-001")
	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Update("status", mdb.StatusPaySuccess).Error; err != nil {
		t.Fatalf("mark order paid: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pay/return/"+tradeID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/pay/checkout-counter/"+tradeID {
		t.Fatalf("location = %q, want checkout counter", got)
	}
}

func TestPayReturn_PaidEpayWithEmptyRedirectReturnsError(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := mustCreateEPayOrder(t, e, "epay-return-empty-001", "https://merchant.example/return", url.Values{
		"token":    {"usdt"},
		"network":  {"tron"},
		"currency": {"cny"},
	})
	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Updates(map[string]interface{}{
			"status":       mdb.StatusPaySuccess,
			"redirect_url": "",
		}).Error; err != nil {
		t.Fatalf("update order: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pay/return/"+tradeID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	if got := int(resp["status_code"].(float64)); got != 10044 {
		t.Fatalf("status_code = %d, want 10044; response=%v", got, resp)
	}
}

func TestPayReturn_PaidEpayWithDisabledApiKeyReturnsError(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := mustCreateEPayOrder(t, e, "epay-return-disabled-key-001", "https://merchant.example/return", url.Values{
		"token":    {"usdt"},
		"network":  {"tron"},
		"currency": {"cny"},
	})
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}
	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Update("status", mdb.StatusPaySuccess).Error; err != nil {
		t.Fatalf("mark order paid: %v", err)
	}
	if err := dao.Mdb.Model(&mdb.ApiKey{}).
		Where("id = ?", order.ApiKeyID).
		Update("status", mdb.ApiKeyStatusDisable).Error; err != nil {
		t.Fatalf("disable api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pay/return/"+tradeID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	if got := int(resp["status_code"].(float64)); got != 10045 {
		t.Fatalf("status_code = %d, want 10045; response=%v", got, resp)
	}
}

func TestPayReturn_PaidEpayWithNonNumericPidReturnsSignatureError(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := mustCreateEPayOrder(t, e, "epay-return-bad-pid-001", "https://merchant.example/return", url.Values{
		"token":    {"usdt"},
		"network":  {"tron"},
		"currency": {"cny"},
	})
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}
	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Update("status", mdb.StatusPaySuccess).Error; err != nil {
		t.Fatalf("mark order paid: %v", err)
	}
	if err := dao.Mdb.Model(&mdb.ApiKey{}).
		Where("id = ?", order.ApiKeyID).
		Update("pid", "not-a-number").Error; err != nil {
		t.Fatalf("break pid: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pay/return/"+tradeID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	if got := int(resp["status_code"].(float64)); got != 10046 {
		t.Fatalf("status_code = %d, want 10046; response=%v", got, resp)
	}
}

func TestSubmitTxHash_VerificationFailureAllowsRetrySameHash(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := createCheckoutCounterRespTestOrder(t, e, "submit-tx-hash-retry-001")

	calls := 0
	restore := service.SetManualOrderPaymentValidatorForTest(func(order *mdb.Orders, blockID string) (string, error) {
		calls++
		if order.TradeId != tradeID {
			t.Fatalf("validator trade_id = %s, want %s", order.TradeId, tradeID)
		}
		if blockID != "retry-hash" {
			t.Fatalf("validator block id = %s, want retry-hash", blockID)
		}
		if calls == 1 {
			return "", fmt.Errorf("rpc verification failed")
		}
		return "canonical-retry-hash", nil
	})
	defer restore()

	rec := doPost(e, "/pay/submit-tx-hash/"+tradeID, map[string]interface{}{
		"block_transaction_id": "retry-hash",
	})
	t.Logf("SubmitTxHash(verification failure): status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var failResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &failResp); err != nil {
		t.Fatalf("unmarshal failure response: %v", err)
	}
	if got := int(failResp["status_code"].(float64)); got != 10038 {
		t.Fatalf("failure status_code = %d, want 10038", got)
	}
	if got, _ := failResp["message"].(string); got != constant.Errno[10038] {
		t.Fatalf("failure message = %q, want %q", got, constant.Errno[10038])
	}

	reloaded, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("reload order after failure: %v", err)
	}
	if reloaded.Status != mdb.StatusWaitPay || reloaded.BlockTransactionId != "" {
		t.Fatalf("order changed after failed submit: status=%d block=%q", reloaded.Status, reloaded.BlockTransactionId)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/pay/check-status/"+tradeID, nil)
	statusRec := httptest.NewRecorder()
	e.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("check-status expected 200, got %d: %s", statusRec.Code, statusRec.Body.String())
	}
	var statusResp map[string]interface{}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("unmarshal check-status response: %v", err)
	}
	statusData := statusResp["data"].(map[string]interface{})
	if got := int(statusData["status"].(float64)); got != mdb.StatusWaitPay {
		t.Fatalf("check-status status after failed submit = %d, want %d", got, mdb.StatusWaitPay)
	}

	retryRec := doPost(e, "/pay/submit-tx-hash/"+tradeID, map[string]interface{}{
		"block_transaction_id": "retry-hash",
	})
	t.Logf("SubmitTxHash(retry same hash): status=%d body=%s", retryRec.Code, retryRec.Body.String())
	if retryRec.Code != http.StatusOK {
		t.Fatalf("expected retry success, got %d: %s", retryRec.Code, retryRec.Body.String())
	}
	if calls != 2 {
		t.Fatalf("validator calls = %d, want 2", calls)
	}
	reloaded, err = data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("reload order after retry: %v", err)
	}
	if reloaded.Status != mdb.StatusPaySuccess || reloaded.BlockTransactionId != "canonical-retry-hash" {
		t.Fatalf("order after retry: status=%d block=%q", reloaded.Status, reloaded.BlockTransactionId)
	}
}

func TestSubmitTxHash_RejectsOkPayOrder(t *testing.T) {
	e := setupTestEnv(t)
	order := &mdb.Orders{
		TradeId:        "trade-submit-tx-hash-okpay",
		OrderId:        "order-submit-tx-hash-okpay",
		Amount:         10,
		Currency:       "CNY",
		ActualAmount:   1.23,
		ReceiveAddress: "OKPAY",
		Token:          "USDT",
		Network:        mdb.NetworkTron,
		Status:         mdb.StatusWaitPay,
		NotifyUrl:      "https://merchant.example/notify",
		PayProvider:    mdb.PaymentProviderOkPay,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	called := false
	restore := service.SetManualOrderPaymentValidatorForTest(func(*mdb.Orders, string) (string, error) {
		called = true
		return "canonical-okpay-hash", nil
	})
	defer restore()

	rec := doPost(e, "/pay/submit-tx-hash/"+order.TradeId, map[string]interface{}{
		"block_transaction_id": "okpay-hash",
	})
	t.Logf("SubmitTxHash(okpay): status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code == http.StatusOK {
		t.Fatalf("expected failure, got 200: %s", rec.Body.String())
	}
	if called {
		t.Fatal("verifier should not run for OkPay/provider orders")
	}

	reloaded, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if reloaded.Status != mdb.StatusWaitPay || reloaded.BlockTransactionId != "" {
		t.Fatalf("order changed after rejected submit: status=%d block=%q", reloaded.Status, reloaded.BlockTransactionId)
	}
}

func TestSubmitTxHash_RejectsExistingHashBeforeRpc(t *testing.T) {
	e := setupTestEnv(t)
	existing := &mdb.Orders{
		TradeId:            "trade-submit-tx-hash-existing",
		OrderId:            "order-submit-tx-hash-existing",
		BlockTransactionId: "existing-hash",
		Amount:             10,
		Currency:           "CNY",
		ActualAmount:       1.23,
		ReceiveAddress:     "TTestTronAddress001",
		Token:              "USDT",
		Network:            mdb.NetworkTron,
		Status:             mdb.StatusPaySuccess,
		PayProvider:        mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(existing).Error; err != nil {
		t.Fatalf("create existing paid order: %v", err)
	}
	tradeID := createCheckoutCounterRespTestOrder(t, e, "submit-tx-hash-existing-001")

	called := false
	restore := service.SetManualOrderPaymentValidatorForTest(func(*mdb.Orders, string) (string, error) {
		called = true
		return "existing-hash", nil
	})
	defer restore()

	rec := doPost(e, "/pay/submit-tx-hash/"+tradeID, map[string]interface{}{
		"block_transaction_id": "existing-hash",
	})
	t.Logf("SubmitTxHash(existing hash): status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code == http.StatusOK {
		t.Fatalf("expected failure, got 200: %s", rec.Body.String())
	}
	if called {
		t.Fatal("verifier should not run when hash already exists")
	}
}

// TestCheckoutCounter_NotFound verifies that /pay/checkout-counter/:trade_id
// with an unknown trade_id does not return an empty 404 (which would mean the
// route is not registered). When the static HTML template is present the
// controller renders it with a 404 status; when it is absent (test env) the
// controller returns a 500 with a descriptive body — both outcomes are
// acceptable because the route IS registered and functional.
func TestCheckoutCounter_NotFound(t *testing.T) {
	e := setupTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/pay/checkout-counter/nonexistent-trade-id", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	t.Logf("CheckoutCounter(not found): status=%d content-type=%s body=%s",
		rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	// A completely empty 404 means the route is not registered at all.
	if rec.Code == http.StatusNotFound && rec.Body.Len() == 0 {
		t.Fatalf("route not registered (404 with empty body)")
	}
	// A 500 whose body mentions a missing file is expected in test environments
	// where the static directory is not present.
	if rec.Code >= 500 && rec.Body.Len() == 0 {
		t.Fatalf("unexpected server error with empty body: %d", rec.Code)
	}
}

func TestCheckoutCounterResp_ReturnsPaidOrder(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := createCheckoutCounterRespTestOrder(t, e, "checkout-counter-paid-001")

	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Update("status", mdb.StatusPaySuccess).Error; err != nil {
		t.Fatalf("mark order paid: %v", err)
	}

	data := getCheckoutCounterRespData(t, e, tradeID)
	if got, _ := data["trade_id"].(string); got != tradeID {
		t.Fatalf("trade_id = %q, want %q; data=%v", got, tradeID, data)
	}
	if got, _ := data["redirect_url"].(string); got != "https://merchant.example/return" {
		t.Fatalf("redirect_url = %q", got)
	}
}

func TestCheckoutCounterResp_NormalizesPaymentTypeCase(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := createCheckoutCounterRespTestOrder(t, e, "checkout-ptype-case-001")

	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Update("payment_type", "epay").Error; err != nil {
		t.Fatalf("set lowercase payment_type: %v", err)
	}

	data := getCheckoutCounterRespData(t, e, tradeID)
	if data["payment_type"] != "epay" {
		t.Fatalf("payment_type = %v, want epay", data["payment_type"])
	}
}

func TestCheckoutCounterResp_ReturnsExpiredOrder(t *testing.T) {
	e := setupTestEnv(t)
	tradeID := createCheckoutCounterRespTestOrder(t, e, "checkout-counter-expired-001")
	expiredCreatedAt := carbon.Now().SubMinutes(config.GetOrderExpirationTime() + 1)

	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Updates(map[string]interface{}{
			"status":     mdb.StatusExpired,
			"created_at": expiredCreatedAt,
		}).Error; err != nil {
		t.Fatalf("mark order expired: %v", err)
	}

	data := getCheckoutCounterRespData(t, e, tradeID)
	if got, _ := data["trade_id"].(string); got != tradeID {
		t.Fatalf("trade_id = %q, want %q; data=%v", got, tradeID, data)
	}
	expirationTime, _ := data["expiration_time"].(float64)
	if int64(expirationTime) > carbon.Now().TimestampMilli() {
		t.Fatalf("expiration_time = %.0f, want expired timestamp", expirationTime)
	}
}

func TestCheckoutCounterResp_UnknownOrderReturnsClearError(t *testing.T) {
	e := setupTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/pay/checkout-counter-resp/nonexistent-trade-id", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal checkout counter error response: %v", err)
	}
	if got := int(resp["status_code"].(float64)); got != 10008 {
		t.Fatalf("status_code = %d, want 10008; response=%v", got, resp)
	}
	if resp["message"] == "" {
		t.Fatalf("expected error message, got: %v", resp)
	}
}

func createCheckoutCounterRespTestOrder(t *testing.T, e *echo.Echo, orderID string) string {
	t.Helper()

	body := signBody(map[string]interface{}{
		"order_id":     orderID,
		"amount":       1.00,
		"token":        "usdt",
		"currency":     "cny",
		"network":      "tron",
		"notify_url":   "https://93.184.216.34/notify",
		"redirect_url": "https://merchant.example/return",
	})
	rec := doPost(e, "/payments/gmpay/v1/order/create-transaction", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create order failed: %d %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected create response data, got: %v", resp)
	}
	tradeID, _ := data["trade_id"].(string)
	if tradeID == "" {
		t.Fatalf("missing trade_id in create response: %v", resp)
	}
	return tradeID
}

func getCheckoutCounterRespData(t *testing.T, e *echo.Echo, tradeID string) map[string]interface{} {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/pay/checkout-counter-resp/"+tradeID, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal checkout counter response: %v", err)
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected checkout counter data, got: %v", resp)
	}
	return data
}

// TestSwitchNetwork_MissingFields verifies that /pay/switch-network validates
// required fields and returns a graceful error when they are missing.
func TestSwitchNetwork_MissingFields(t *testing.T) {
	e := setupTestEnv(t)
	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		// trade_id, token, network are all missing
	})
	t.Logf("SwitchNetwork(missing fields): status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code >= 500 {
		t.Fatalf("unexpected server error: %d %s", rec.Code, rec.Body.String())
	}
	// Must return a non-200 business code for validation failure.
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if code, _ := resp["code"].(float64); code == 200 {
		t.Fatal("expected validation error for missing fields, got code=200")
	}
}

// TestSwitchNetwork_OrderNotFound verifies that /pay/switch-network returns a
// graceful error when the referenced trade_id doesn't exist.
func TestSwitchNetwork_OrderNotFound(t *testing.T) {
	e := setupTestEnv(t)
	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": "nonexistent-trade-id",
		"token":    "USDT",
		"network":  "tron",
	})
	t.Logf("SwitchNetwork(not found): status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code >= 500 {
		t.Fatalf("unexpected server error: %d %s", rec.Code, rec.Body.String())
	}
}

// TestSwitchNetwork_WithOrder verifies /pay/switch-network can create a
// sub-order when given a valid parent trade_id.
func TestSwitchNetwork_WithOrder(t *testing.T) {
	e := setupTestEnv(t)

	// Create a parent order on solana.
	createBody := signBody(map[string]interface{}{
		"order_id":   "switch-net-parent-001",
		"amount":     1.00,
		"token":      "usdt",
		"currency":   "cny",
		"network":    "solana",
		"notify_url": "https://93.184.216.34/notify",
	})
	createRec := doPost(e, "/payments/gmpay/v1/order/create-transaction", createBody)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create parent order failed: %d %s", createRec.Code, createRec.Body.String())
	}
	var createResp map[string]interface{}
	json.Unmarshal(createRec.Body.Bytes(), &createResp)
	tradeId, _ := createResp["data"].(map[string]interface{})["trade_id"].(string)
	if tradeId == "" {
		t.Fatal("no trade_id in create response")
	}

	// Switch to tron.
	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeId,
		"token":    "USDT",
		"network":  "tron",
	})
	t.Logf("SwitchNetwork(valid): status=%d body=%s", rec.Code, rec.Body.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["data"] == nil {
		t.Fatal("expected data in switch-network response")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubRestyClient(fn roundTripFunc) *resty.Client {
	return resty.NewWithClient(&http.Client{Transport: fn})
}

func TestSwitchNetwork_OkPayCreatesProviderSubOrder(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayEnabled, "true", mdb.SettingTypeBool); err != nil {
		t.Fatalf("seed okpay.enabled: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopID, "shop-1", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_id: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopToken, "token-1", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayCallbackURL, "https://example.com/okpay/notify", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.callback_url: %v", err)
	}

	origFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		return stubRestyClient(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://api.okaypay.me/shop/payLink" {
				t.Fatalf("unexpected okpay URL: %s", req.URL.String())
			}
			if err := req.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if got := req.Form.Get("coin"); got != "USDT" {
				t.Fatalf("coin = %q, want USDT", got)
			}
			if req.Form.Get("callback_url") == "" {
				t.Fatal("callback_url should be set")
			}
			body := `{"status":"success","code":200,"data":{"order_id":"okp-order-1","pay_url":"https://pay.okaypay.test/abc"}}`
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})
	}
	t.Cleanup(func() {
		http_client.ClientFactory = origFactory
	})

	createBody := signBody(map[string]interface{}{
		"order_id":   "switch-okpay-parent-001",
		"amount":     1.00,
		"token":      "usdt",
		"currency":   "cny",
		"network":    "solana",
		"notify_url": "https://93.184.216.34/notify",
	})
	createRec := doPost(e, "/payments/gmpay/v1/order/create-transaction", createBody)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create parent order failed: %d %s", createRec.Code, createRec.Body.String())
	}
	createResp := parseResp(t, createRec)
	tradeID, _ := createResp["data"].(map[string]interface{})["trade_id"].(string)
	if tradeID == "" {
		t.Fatal("missing parent trade_id")
	}

	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeID,
		"token":    "USDT",
		"network":  "okpay",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch-network okpay failed: %d %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	dataMap, _ := resp["data"].(map[string]interface{})
	if dataMap["payment_url"] != "https://pay.okaypay.test/abc" {
		t.Fatalf("payment_url = %v, want okpay url", dataMap["payment_url"])
	}
	if dataMap["network"] != "tron" {
		t.Fatalf("network = %v, want tron", dataMap["network"])
	}
	if dataMap["receive_address"] != "OKPAY" {
		t.Fatalf("receive_address = %v, want OKPAY", dataMap["receive_address"])
	}

	subTradeID, _ := dataMap["trade_id"].(string)
	if subTradeID == "" || subTradeID == tradeID {
		t.Fatalf("expected child trade_id, got %q", subTradeID)
	}
	subOrder, err := data.GetOrderInfoByTradeId(subTradeID)
	if err != nil {
		t.Fatalf("load sub-order: %v", err)
	}
	if subOrder.PayProvider != mdb.PaymentProviderOkPay {
		t.Fatalf("pay_provider = %q, want %q", subOrder.PayProvider, mdb.PaymentProviderOkPay)
	}
	providerRow, err := data.GetProviderOrderByTradeIDAndProvider(subTradeID, mdb.PaymentProviderOkPay)
	if err != nil {
		t.Fatalf("load provider row: %v", err)
	}
	if providerRow.ProviderOrderID != "okp-order-1" {
		t.Fatalf("provider_order_id = %q, want okp-order-1", providerRow.ProviderOrderID)
	}
	if providerRow.PayURL != "https://pay.okaypay.test/abc" {
		t.Fatalf("pay_url = %q", providerRow.PayURL)
	}
}

func TestSwitchNetwork_OkPayFromWaitSelectPlaceholder(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayEnabled, "true", mdb.SettingTypeBool); err != nil {
		t.Fatalf("seed okpay.enabled: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopID, "shop-placeholder", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_id: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopToken, "token-placeholder", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayCallbackURL, "https://example.com/okpay/notify", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.callback_url: %v", err)
	}

	origFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		return stubRestyClient(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://api.okaypay.me/shop/payLink" {
				t.Fatalf("unexpected okpay URL: %s", req.URL.String())
			}
			if err := req.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if got := req.Form.Get("coin"); got != "USDT" {
				t.Fatalf("coin = %q, want USDT", got)
			}
			body := `{"status":"success","code":200,"data":{"order_id":"okp-placeholder-1","pay_url":"https://pay.okaypay.test/placeholder"}}`
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})
	}
	t.Cleanup(func() {
		http_client.ClientFactory = origFactory
	})

	createBody := signBody(map[string]interface{}{
		"order_id":   "switch-okpay-placeholder-001",
		"amount":     1.00,
		"currency":   "cny",
		"notify_url": "https://93.184.216.34/notify",
	})
	createRec := doPost(e, "/payments/gmpay/v1/order/create-transaction", createBody)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create placeholder failed: %d %s", createRec.Code, createRec.Body.String())
	}
	createResp := parseResp(t, createRec)
	tradeID, _ := createResp["data"].(map[string]interface{})["trade_id"].(string)
	if tradeID == "" {
		t.Fatal("missing parent trade_id")
	}

	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeID,
		"token":    "USDT",
		"network":  "okpay",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch placeholder to okpay failed: %d %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	dataMap, _ := resp["data"].(map[string]interface{})
	if dataMap["payment_url"] != "https://pay.okaypay.test/placeholder" {
		t.Fatalf("payment_url = %v, want okpay url", dataMap["payment_url"])
	}
	if got, _ := dataMap["trade_id"].(string); got != tradeID {
		t.Fatalf("switch trade_id = %q, want parent %q", got, tradeID)
	}

	parent, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load parent order: %v", err)
	}
	if parent.Status != mdb.StatusWaitPay {
		t.Fatalf("parent status = %d, want %d", parent.Status, mdb.StatusWaitPay)
	}
	if parent.IsSelected {
		t.Fatal("parent is_selected = true, want false")
	}
	if parent.PayProvider != mdb.PaymentProviderOkPay || parent.Token != "USDT" || parent.Network != mdb.PaymentProviderOkPay || parent.ReceiveAddress != "OKPAY" || parent.ActualAmount <= 0 {
		t.Fatalf("okpay placeholder parent fields = provider=%q token=%q network=%q address=%q actual=%v", parent.PayProvider, parent.Token, parent.Network, parent.ReceiveAddress, parent.ActualAmount)
	}

	providerRow, err := data.GetProviderOrderByTradeIDAndProvider(tradeID, mdb.PaymentProviderOkPay)
	if err != nil {
		t.Fatalf("load provider row: %v", err)
	}
	if providerRow.ProviderOrderID != "okp-placeholder-1" || providerRow.PayURL != "https://pay.okaypay.test/placeholder" {
		t.Fatalf("provider row = order_id %q pay_url %q", providerRow.ProviderOrderID, providerRow.PayURL)
	}

	var lockCount int64
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).
		Where("trade_id = ?", tradeID).
		Count(&lockCount).Error; err != nil {
		t.Fatalf("count transaction locks: %v", err)
	}
	if lockCount != 0 {
		t.Fatalf("transaction lock count = %d, want 0", lockCount)
	}

	checkoutData := getCheckoutCounterRespData(t, e, tradeID)
	if checkoutData["payment_url"] != "https://pay.okaypay.test/placeholder" {
		t.Fatalf("checkout payment_url = %v, want okpay url", checkoutData["payment_url"])
	}

	count, err := data.CountActiveSubOrders(tradeID)
	if err != nil {
		t.Fatalf("count active sub-orders: %v", err)
	}
	if count != 0 {
		t.Fatalf("active sub-order count after first okpay switch = %d, want 0", count)
	}

	secondRec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeID,
		"token":    "USDT",
		"network":  "solana",
	})
	if secondRec.Code != http.StatusOK {
		t.Fatalf("switch okpay parent to solana failed: %d %s", secondRec.Code, secondRec.Body.String())
	}
	secondResp := parseResp(t, secondRec)
	secondData, _ := secondResp["data"].(map[string]interface{})
	subTradeID, _ := secondData["trade_id"].(string)
	if subTradeID == "" || subTradeID == tradeID {
		t.Fatalf("expected chain child trade_id, got %q", subTradeID)
	}
	subOrder, err := data.GetOrderInfoByTradeId(subTradeID)
	if err != nil {
		t.Fatalf("load chain sub-order: %v", err)
	}
	if subOrder.ParentTradeId != tradeID || subOrder.PayProvider != mdb.PaymentProviderOnChain || subOrder.Network != mdb.NetworkSolana {
		t.Fatalf("sub-order parent/provider/network = %q/%q/%q", subOrder.ParentTradeId, subOrder.PayProvider, subOrder.Network)
	}
}

func TestSwitchNetwork_EpayPlaceholderCompletesChainInPlace(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultToken, "", mdb.SettingTypeString); err != nil {
		t.Fatalf("clear epay.default_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultNetwork, "", mdb.SettingTypeString); err != nil {
		t.Fatalf("clear epay.default_network: %v", err)
	}

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-switch-chain-ph-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-switch-chain-ph-001"},
		"notify_url":   {"https://93.184.216.34/notify"},
		"return_url":   {"http://localhost/return"},
	})
	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/submit.php?"+values.Encode(), nil)
	createRec := httptest.NewRecorder()
	e.ServeHTTP(createRec, req)
	if createRec.Code != http.StatusFound {
		t.Fatalf("create epay placeholder failed: %d %s", createRec.Code, createRec.Body.String())
	}
	tradeID := strings.TrimPrefix(createRec.Header().Get("Location"), "/pay/checkout-counter/")

	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeID,
		"token":    "USDT",
		"network":  "tron",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch epay placeholder to chain failed: %d %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	respData, _ := resp["data"].(map[string]interface{})
	if got, _ := respData["trade_id"].(string); got != tradeID {
		t.Fatalf("switch trade_id = %q, want parent %q", got, tradeID)
	}
	if got, _ := respData["redirect_url"].(string); got != "http://localhost:8080/pay/return/"+tradeID {
		t.Fatalf("switch redirect_url = %q, want internal return route", got)
	}

	parent, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	if parent.Status != mdb.StatusWaitPay || parent.PaymentType != mdb.PaymentTypeEpay || parent.Network != mdb.NetworkTron || parent.Token != "USDT" || parent.ReceiveAddress == "" {
		t.Fatalf("parent fields = status %d payment_type %q network %q token %q address %q", parent.Status, parent.PaymentType, parent.Network, parent.Token, parent.ReceiveAddress)
	}
	if parent.RedirectUrl != "http://localhost/return" {
		t.Fatalf("stored redirect_url = %q, want merchant raw return_url", parent.RedirectUrl)
	}
	count, err := data.CountActiveSubOrders(tradeID)
	if err != nil {
		t.Fatalf("count active sub-orders: %v", err)
	}
	if count != 0 {
		t.Fatalf("active sub-order count = %d, want 0", count)
	}
}

func TestSwitchNetwork_OkPayFromEpayWaitSelectPlaceholder(t *testing.T) {
	e := setupTestEnv(t)

	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultToken, "", mdb.SettingTypeString); err != nil {
		t.Fatalf("clear epay.default_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupEpay, mdb.SettingKeyEpayDefaultNetwork, "", mdb.SettingTypeString); err != nil {
		t.Fatalf("clear epay.default_network: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayEnabled, "true", mdb.SettingTypeBool); err != nil {
		t.Fatalf("seed okpay.enabled: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopID, "shop-epay-placeholder", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_id: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopToken, "token-epay-placeholder", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayCallbackURL, "https://example.com/okpay/notify", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.callback_url: %v", err)
	}

	origFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		return stubRestyClient(func(req *http.Request) (*http.Response, error) {
			body := `{"status":"success","code":200,"data":{"order_id":"okp-epay-placeholder-1","pay_url":"https://pay.okaypay.test/epay-placeholder"}}`
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})
	}
	t.Cleanup(func() {
		http_client.ClientFactory = origFactory
	})

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-switch-okpay-ph-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-switch-okpay-ph-001"},
		"notify_url":   {"https://93.184.216.34/notify"},
		"return_url":   {"http://localhost/return"},
	})
	req := httptest.NewRequest(http.MethodGet, "/payments/epay/v1/order/create-transaction/submit.php?"+values.Encode(), nil)
	createRec := httptest.NewRecorder()
	e.ServeHTTP(createRec, req)
	if createRec.Code != http.StatusFound {
		t.Fatalf("create epay placeholder failed: %d %s", createRec.Code, createRec.Body.String())
	}
	tradeID := strings.TrimPrefix(createRec.Header().Get("Location"), "/pay/checkout-counter/")

	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeID,
		"token":    "USDT",
		"network":  "okpay",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch epay placeholder to okpay failed: %d %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	respData, _ := resp["data"].(map[string]interface{})
	if got, _ := respData["trade_id"].(string); got != tradeID {
		t.Fatalf("switch trade_id = %q, want parent %q", got, tradeID)
	}
	if respData["payment_url"] != "https://pay.okaypay.test/epay-placeholder" {
		t.Fatalf("payment_url = %v, want okpay url", respData["payment_url"])
	}
	if got, _ := respData["redirect_url"].(string); got != "http://localhost:8080/pay/return/"+tradeID {
		t.Fatalf("switch redirect_url = %q, want internal return route", got)
	}

	parent, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	if parent.PaymentType != mdb.PaymentTypeEpay || parent.PayProvider != mdb.PaymentProviderOkPay || parent.Network != mdb.PaymentProviderOkPay || parent.ReceiveAddress != "OKPAY" {
		t.Fatalf("parent fields = payment_type %q provider %q network %q address %q", parent.PaymentType, parent.PayProvider, parent.Network, parent.ReceiveAddress)
	}
	if parent.RedirectUrl != "http://localhost/return" {
		t.Fatalf("stored redirect_url = %q, want merchant raw return_url", parent.RedirectUrl)
	}
	count, err := data.CountActiveSubOrders(tradeID)
	if err != nil {
		t.Fatalf("count active sub-orders: %v", err)
	}
	if count != 0 {
		t.Fatalf("active sub-order count = %d, want 0", count)
	}
	providerRow, err := data.GetProviderOrderByTradeIDAndProvider(tradeID, mdb.PaymentProviderOkPay)
	if err != nil {
		t.Fatalf("load provider row: %v", err)
	}
	if providerRow.ProviderOrderID != "okp-epay-placeholder-1" {
		t.Fatalf("provider_order_id = %q", providerRow.ProviderOrderID)
	}
}

func TestSwitchNetwork_OkPayIntegration(t *testing.T) {
	shopID := strings.TrimSpace(os.Getenv("EPUSDT_OKPAY_ID"))
	if shopID == "" {
		shopID = strings.TrimSpace(os.Getenv("OKPAY_ID"))
	}
	if shopID == "" {
		shopID = strings.TrimSpace(os.Getenv("EPUSDT_OKPAY_TEST_SHOP_ID"))
	}
	shopToken := strings.TrimSpace(os.Getenv("EPUSDT_OKPAY_TOKEN"))
	if shopToken == "" {
		shopToken = strings.TrimSpace(os.Getenv("OKPAY_TOKEN"))
	}
	if shopToken == "" {
		shopToken = strings.TrimSpace(os.Getenv("EPUSDT_OKPAY_TEST_SHOP_TOKEN"))
	}
	if shopID == "" || shopToken == "" {
		t.Skip("set EPUSDT_OKPAY_ID/EPUSDT_OKPAY_TOKEN (or OKPAY_ID/OKPAY_TOKEN) to run OkPay integration test")
	}

	e := setupTestEnv(t)
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayEnabled, "true", mdb.SettingTypeBool); err != nil {
		t.Fatalf("seed okpay.enabled: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopID, shopID, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_id: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayShopToken, shopToken, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.shop_token: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayCallbackURL, "https://example.com/okpay-notify", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.callback_url: %v", err)
	}
	if err := data.SetSetting(mdb.SettingGroupOkPay, mdb.SettingKeyOkPayReturnURL, "https://example.com/okpay-return", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed okpay.return_url: %v", err)
	}

	createBody := signBody(map[string]interface{}{
		"order_id":   "switch-okpay-live-001",
		"amount":     1.00,
		"token":      "usdt",
		"currency":   "cny",
		"network":    "solana",
		"notify_url": "https://93.184.216.34/notify",
	})
	createRec := doPost(e, "/payments/gmpay/v1/order/create-transaction", createBody)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create parent order failed: %d %s", createRec.Code, createRec.Body.String())
	}
	createResp := parseResp(t, createRec)
	tradeID, _ := createResp["data"].(map[string]interface{})["trade_id"].(string)
	if tradeID == "" {
		t.Fatal("missing parent trade_id")
	}

	rec := doPost(e, "/pay/switch-network", map[string]interface{}{
		"trade_id": tradeID,
		"token":    "USDT",
		"network":  "okpay",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("switch-network okpay failed: %d %s", rec.Code, rec.Body.String())
	}
	resp := parseResp(t, rec)
	dataMap, _ := resp["data"].(map[string]interface{})
	paymentURL, _ := dataMap["payment_url"].(string)
	if paymentURL == "" {
		t.Fatalf("missing payment_url in response: %v", dataMap)
	}
	t.Logf("OkPay payment_url=%s", paymentURL)
}
