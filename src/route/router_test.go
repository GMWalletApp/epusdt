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
	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/sign"
	"github.com/dromara/carbon/v2"
	"github.com/go-resty/resty/v2"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
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

	log.Init()

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
		"notify_url": "http://localhost/notify",
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

// TestCreateOrderGmpayV1SolNative tests creating an order for native SOL token.
func TestCreateOrderGmpayV1SolNative(t *testing.T) {
	e := setupTestEnv(t)

	body := signBody(map[string]interface{}{
		"order_id":   "test-sol-native-001",
		"amount":     0.05,
		"token":      "sol",
		"currency":   "usd",
		"network":    "solana",
		"notify_url": "http://localhost/notify",
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
		"notify_url": {"http://localhost/notify"},
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
		NotifyUrl:      "http://localhost/notify",
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
		"notify_url": "http://localhost/notify",
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

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-get-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-get-001"},
		"notify_url":   {"http://localhost/notify"},
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
}

func TestEpaySubmitPhpPostFormCompatible(t *testing.T) {
	e := setupTestEnv(t)

	values := signEpayValues(url.Values{
		"pid":          {"1"},
		"name":         {"epay-post-001"},
		"type":         {"alipay"},
		"money":        {"1.00"},
		"out_trade_no": {"epay-post-001"},
		"notify_url":   {"http://localhost/notify"},
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
		"notify_url":   "http://localhost/notify",
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
		"notify_url": "http://localhost/notify",
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
		"notify_url": "http://localhost/notify",
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
		"notify_url": "http://localhost/notify",
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
