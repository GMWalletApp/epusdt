package service

import (
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/go-resty/resty/v2"
	"github.com/xssnick/tonutils-go/address"
)

func newCreateTransactionRequest(orderID string, amount float64) *request.CreateTransactionRequest {
	return &request.CreateTransactionRequest{
		OrderId:   orderID,
		Currency:  "CNY",
		Token:     "USDT",
		Network:   "tron",
		Amount:    amount,
		NotifyUrl: "https://93.184.216.34/callback",
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGenerateCodeFormat(t *testing.T) {
	tradeID := GenerateCode()

	if len(tradeID) != 24 {
		t.Fatalf("trade id length = %d, want 24", len(tradeID))
	}
	if strings.Contains(tradeID, "=") {
		t.Fatalf("trade id = %q, contains padding '='", tradeID)
	}
	for _, ch := range tradeID {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		t.Fatalf("trade id = %q, contains non URL-safe character %q", tradeID, ch)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(tradeID)
	if err != nil {
		t.Fatalf("decode trade id %q: %v", tradeID, err)
	}
	if len(decoded) != 18 {
		t.Fatalf("decoded trade id length = %d, want 18", len(decoded))
	}
}

func installMockHTTPClient(t *testing.T, handler roundTripFunc) {
	t.Helper()

	oldFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		client := resty.NewWithClient(&http.Client{Transport: handler})
		client.SetTimeout(10 * time.Second)
		return client
	}
	t.Cleanup(func() {
		http_client.ClientFactory = oldFactory
	})
}

func TestCreateTransactionRejectsPrivateNotifyURL(t *testing.T) {
	req := newCreateTransactionRequest("order_private_notify_url", 1)
	req.NotifyUrl = "http://127.0.0.1/notify"

	if _, err := CreateTransaction(req, nil); err != constant.NotifyURLErr {
		t.Fatalf("CreateTransaction error = %v, want %v", err, constant.NotifyURLErr)
	}
}

func TestCreateTransactionCreatesWaitSelectPlaceholderWithoutTokenNetwork(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	req := newCreateTransactionRequest("order_wait_select_1", 12.34)
	req.Token = ""
	req.Network = ""
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create placeholder transaction: %v", err)
	}
	if resp.Status != mdb.StatusWaitSelect {
		t.Fatalf("response status = %d, want %d", resp.Status, mdb.StatusWaitSelect)
	}
	if resp.Token != "" || resp.ReceiveAddress != "" || resp.ActualAmount != 0 {
		t.Fatalf("placeholder chain fields = token %q address %q actual %.4f", resp.Token, resp.ReceiveAddress, resp.ActualAmount)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload placeholder order: %v", err)
	}
	if order.Status != mdb.StatusWaitSelect {
		t.Fatalf("order status = %d, want %d", order.Status, mdb.StatusWaitSelect)
	}
	if order.PaymentType != mdb.PaymentTypeGmpay {
		t.Fatalf("placeholder payment_type = %q, want %q", order.PaymentType, mdb.PaymentTypeGmpay)
	}
	if order.Token != "" || order.Network != "" || order.ReceiveAddress != "" || order.ActualAmount != 0 {
		t.Fatalf("placeholder order has chain fields: %+v", order)
	}

	var locks int64
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).Where("trade_id = ?", resp.TradeId).Count(&locks).Error; err != nil {
		t.Fatalf("count transaction locks: %v", err)
	}
	if locks != 0 {
		t.Fatalf("placeholder lock count = %d, want 0", locks)
	}
}

func TestCreateTransactionRejectsPartialTokenNetwork(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	req := newCreateTransactionRequest("order_partial_token_network_1", 1)
	req.Token = ""
	if _, err := CreateTransaction(req, nil); err != constant.ParamsMarshalErr {
		t.Fatalf("missing token error = %v, want %v", err, constant.ParamsMarshalErr)
	}

	req = newCreateTransactionRequest("order_partial_token_network_2", 1)
	req.Network = ""
	if _, err := CreateTransaction(req, nil); err != constant.ParamsMarshalErr {
		t.Fatalf("missing network error = %v, want %v", err, constant.ParamsMarshalErr)
	}
}

func TestCreateTransactionNormalizesEpayPaymentTypeCase(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	req := newCreateTransactionRequest("order_epay_payment_type_1", 1)
	req.Token = ""
	req.Network = ""
	req.PaymentType = "epay"
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create epay-compatible transaction: %v", err)
	}
	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if order.PaymentType != mdb.PaymentTypeEpay {
		t.Fatalf("payment_type = %q, want %q", order.PaymentType, mdb.PaymentTypeEpay)
	}
}

func TestCreateTransactionAssignsIncrementedAmountsAndLocks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp1, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create first transaction: %v", err)
	}
	resp2, err := CreateTransaction(newCreateTransactionRequest("order_2", 1), nil)
	if err != nil {
		t.Fatalf("create second transaction: %v", err)
	}

	if got := fmt.Sprintf("%.2f", resp1.ActualAmount); got != "1.00" {
		t.Fatalf("first actual amount = %s, want 1.00", got)
	}
	if got := fmt.Sprintf("%.2f", resp2.ActualAmount); got != "1.01" {
		t.Fatalf("second actual amount = %s, want 1.01", got)
	}
	if resp1.ReceiveAddress != "wallet_1" || resp2.ReceiveAddress != "wallet_1" {
		t.Fatalf("unexpected receive addresses: %s, %s", resp1.ReceiveAddress, resp2.ReceiveAddress)
	}
	if resp1.Token != "USDT" || resp2.Token != "USDT" {
		t.Fatalf("unexpected tokens: %s, %s", resp1.Token, resp2.Token)
	}

	tradeID1, err := data.GetTradeIdByWalletAddressAndAmountAndToken("tron", resp1.ReceiveAddress, resp1.Token, resp1.ActualAmount)
	if err != nil {
		t.Fatalf("get first runtime lock: %v", err)
	}
	if tradeID1 != resp1.TradeId {
		t.Fatalf("first runtime lock = %s, want %s", tradeID1, resp1.TradeId)
	}

	tradeID2, err := data.GetTradeIdByWalletAddressAndAmountAndToken("tron", resp2.ReceiveAddress, resp2.Token, resp2.ActualAmount)
	if err != nil {
		t.Fatalf("get second runtime lock: %v", err)
	}
	if tradeID2 != resp2.TradeId {
		t.Fatalf("second runtime lock = %s, want %s", tradeID2, resp2.TradeId)
	}
}

func TestCreateTransactionUsesConfiguredAmountPrecision(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "4", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set amount precision: %v", err)
	}
	if _, err := data.AddWalletAddress("wallet_precision_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp1, err := CreateTransaction(newCreateTransactionRequest("order_precision_1", 1), nil)
	if err != nil {
		t.Fatalf("create first transaction: %v", err)
	}
	resp2, err := CreateTransaction(newCreateTransactionRequest("order_precision_2", 1), nil)
	if err != nil {
		t.Fatalf("create second transaction: %v", err)
	}

	if got := fmt.Sprintf("%.4f", resp1.ActualAmount); got != "1.0000" {
		t.Fatalf("first actual amount = %s, want 1.0000", got)
	}
	if got := fmt.Sprintf("%.4f", resp2.ActualAmount); got != "1.0001" {
		t.Fatalf("second actual amount = %s, want 1.0001", got)
	}
}

func TestCreateTransactionStoresNormalizedMerchantAmount(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_normalized_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_normalized_1", 100.129), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if got := fmt.Sprintf("%.2f", resp.Amount); got != "100.13" {
		t.Fatalf("response amount = %s, want 100.13", got)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}
	if got := fmt.Sprintf("%.2f", order.Amount); got != "100.13" {
		t.Fatalf("stored amount = %s, want 100.13", got)
	}
}

func TestCreateTransactionUsesRateAPIWhenForcedSettingIsNotPositive(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/cny.json" {
			t.Fatalf("rate api path = %s, want /cny.json", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.14635}}`)),
			Request:    r,
		}, nil
	})

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0}}`, "json"); err != nil {
		t.Fatalf("set rate.forced_rate_list: %v", err)
	}
	if err := data.SetSetting("rate", "rate.api_url", "https://rate.example.test", "string"); err != nil {
		t.Fatalf("set rate.api_url: %v", err)
	}
	if err := data.SetSetting("rate", "rate.mode", config.RateModeAuto, "string"); err != nil {
		t.Fatalf("set rate.mode: %v", err)
	}
	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_api_rate_1", 10), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if got := fmt.Sprintf("%.2f", resp.ActualAmount); got != "1.46" {
		t.Fatalf("actual amount = %s, want 1.46", got)
	}
}

func TestCreateTransactionUsesDefaultForcedRateWithoutRateAPI(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	t.Setenv("API_RATE_URL", "")

	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("rate API should not be called when default rate.forced_rate_list has positive rate")
		return nil, nil
	})

	if err := data.SetSetting("rate", "rate.forced_rate_list", mdb.SettingDefaultRateForcedRateList, "json"); err != nil {
		t.Fatalf("set rate.forced_rate_list: %v", err)
	}
	if err := data.SetSetting("rate", "rate.api_url", "", "string"); err != nil {
		t.Fatalf("clear rate.api_url: %v", err)
	}
	if _, err := data.AddWalletAddress("wallet_default_rate_usdt"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkSolana, "SolDefaultRateAddress001"); err != nil {
		t.Fatalf("add solana wallet: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.ChainToken{
		Network:         mdb.NetworkSolana,
		Symbol:          "USDC",
		ContractAddress: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		Decimals:        6,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("seed solana USDC token: %v", err)
	}

	usdtResp, err := CreateTransaction(newCreateTransactionRequest("order_default_rate_usdt_1", 10), nil)
	if err != nil {
		t.Fatalf("create USDT transaction: %v", err)
	}
	if got := fmt.Sprintf("%.2f", usdtResp.ActualAmount); got != "1.47" {
		t.Fatalf("USDT actual amount = %s, want 1.47", got)
	}

	usdcReq := newCreateTransactionRequest("order_default_rate_usdc_1", 10)
	usdcReq.Network = mdb.NetworkSolana
	usdcReq.Token = "USDC"
	usdcResp, err := CreateTransaction(usdcReq, nil)
	if err != nil {
		t.Fatalf("create USDC transaction: %v", err)
	}
	if got := fmt.Sprintf("%.2f", usdcResp.ActualAmount); got != "1.47" {
		t.Fatalf("USDC actual amount = %s, want 1.47", got)
	}
}

func TestCreateTransactionFailsWhenRateAPIUnavailableAndForcedSettingIsNotPositive(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	t.Setenv("API_RATE_URL", "")

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0}}`, "json"); err != nil {
		t.Fatalf("set rate.forced_rate_list: %v", err)
	}
	if err := data.SetSetting("rate", "rate.api_url", "", "string"); err != nil {
		t.Fatalf("clear rate.api_url: %v", err)
	}
	if err := data.SetSetting("rate", "rate.mode", config.RateModeAuto, "string"); err != nil {
		t.Fatalf("set rate.mode: %v", err)
	}

	_, err := CreateTransaction(newCreateTransactionRequest("order_missing_rate_1", 10), nil)
	if err != constant.RateAmountErr {
		t.Fatalf("create transaction error = %v, want %v", err, constant.RateAmountErr)
	}
}

func TestCreateTransactionNormalizesEvmReceiveAddressToLowercase(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	mixedAddress := "0xA1B2c3D4e5F60718293aBcDeF001122334455667"
	if err := dao.Mdb.Create(&mdb.WalletAddress{
		Network: mdb.NetworkEthereum,
		Address: mixedAddress,
		Status:  mdb.TokenStatusEnable,
	}).Error; err != nil {
		t.Fatalf("seed mixed-case wallet: %v", err)
	}

	req := newCreateTransactionRequest("order_evm_1", 1)
	req.Network = mdb.NetworkEthereum

	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	expectedAddress := strings.ToLower(mixedAddress)
	if resp.ReceiveAddress != expectedAddress {
		t.Fatalf("receive address = %q, want %q", resp.ReceiveAddress, expectedAddress)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}
	if order.ReceiveAddress != expectedAddress {
		t.Fatalf("stored order address = %q, want %q", order.ReceiveAddress, expectedAddress)
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkEthereum, strings.ToUpper(mixedAddress), resp.Token, resp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup runtime lock: %v", err)
	}
	if tradeID != resp.TradeId {
		t.Fatalf("runtime lock trade_id = %q, want %q", tradeID, resp.TradeId)
	}
}

func TestCreateTransactionCreatesTonOrderAndRawLock(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"ton":0.5}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	addr := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	wallet, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, addr.StringRaw())
	if err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}

	req := newCreateTransactionRequest("order_ton_native_1", 10)
	req.Network = mdb.NetworkTon
	req.Token = "TON"

	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create TON transaction: %v", err)
	}
	if resp.ReceiveAddress != wallet.Address {
		t.Fatalf("receive address = %q, want %q", resp.ReceiveAddress, wallet.Address)
	}
	if resp.Token != "TON" {
		t.Fatalf("token = %q, want TON", resp.Token)
	}
	if got := fmt.Sprintf("%.2f", resp.ActualAmount); got != "5.00" {
		t.Fatalf("actual amount = %s, want 5.00", got)
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, addr.StringRaw(), "TON", resp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup ton runtime lock: %v", err)
	}
	if tradeID != resp.TradeId {
		t.Fatalf("runtime lock trade_id = %q, want %q", tradeID, resp.TradeId)
	}
}

func TestCreateTransactionCreatesTonOrderWithConfiguredPrecision(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "4", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set amount precision: %v", err)
	}
	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"ton":0.12345}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	addr := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, addr.StringRaw()); err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}

	req := newCreateTransactionRequest("order_ton_precision_1", 10)
	req.Network = mdb.NetworkTon
	req.Token = "TON"
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create TON transaction: %v", err)
	}
	if got := fmt.Sprintf("%.4f", resp.ActualAmount); got != "1.2345" {
		t.Fatalf("actual amount = %s, want 1.2345", got)
	}
	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, addr.Bounce(true).String(), "TON", 1.2345)
	if err != nil {
		t.Fatalf("lookup ton runtime lock: %v", err)
	}
	if tradeID != resp.TradeId {
		t.Fatalf("runtime lock trade_id = %q, want %q", tradeID, resp.TradeId)
	}
}

func TestCreateTransactionRejectsUnsupportedTonToken(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	req := newCreateTransactionRequest("order_ton_gram_1", 10)
	req.Network = mdb.NetworkTon
	req.Token = "GRAM"

	_, err := CreateTransaction(req, nil)
	if err != constant.SupportedAssetNotFound {
		t.Fatalf("create unsupported TON token error = %v, want %v", err, constant.SupportedAssetNotFound)
	}
}

func TestCreateTransactionRejectsDisabledTonTokenBeforeRate(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ? AND symbol = ?", mdb.NetworkTon, "TON").
		Update("enabled", false).Error; err != nil {
		t.Fatalf("disable TON token: %v", err)
	}
	req := newCreateTransactionRequest("order_ton_disabled_1", 10)
	req.Network = mdb.NetworkTon
	req.Token = "TON"

	_, err := CreateTransaction(req, nil)
	if err != constant.SupportedAssetNotFound {
		t.Fatalf("create disabled TON token error = %v, want %v", err, constant.SupportedAssetNotFound)
	}
}

func TestCreateTransactionRejectsEnabledMisconfiguredAptosUSDT(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.Chain{Network: mdb.NetworkAptos, Enabled: true}).Error; err != nil {
		t.Fatalf("create aptos chain: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.ChainToken{
		Network:         mdb.NetworkAptos,
		Symbol:          "USDT",
		ContractAddress: "",
		Decimals:        6,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("create aptos usdt token: %v", err)
	}

	req := newCreateTransactionRequest("order_aptos_misconfigured_usdt_1", 10)
	req.Network = mdb.NetworkAptos
	req.Token = "USDT"

	_, err := CreateTransaction(req, nil)
	if err != constant.SupportedAssetNotFound {
		t.Fatalf("create misconfigured Aptos USDT error = %v, want %v", err, constant.SupportedAssetNotFound)
	}
}

func TestEnsureEnabledOrderAssetAllowsConfiguredAptosCustomToken(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.ChainToken{
		Network:         mdb.NetworkAptos,
		Symbol:          "MOVEUSD",
		ContractAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Decimals:        6,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("create aptos custom token: %v", err)
	}

	token, err := ensureEnabledOrderAsset(mdb.NetworkAptos, "MOVEUSD")
	if err != nil {
		t.Fatalf("ensureEnabledOrderAsset(): %v", err)
	}
	if token.Symbol != "MOVEUSD" {
		t.Fatalf("token symbol = %q, want MOVEUSD", token.Symbol)
	}
}

func TestCreateTransactionSupportedTonTokenWithoutRateReturnsRateError(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"ton":0}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	if err := data.SetSetting("rate", "rate.api_url", "", "string"); err != nil {
		t.Fatalf("clear rate api url: %v", err)
	}
	req := newCreateTransactionRequest("order_ton_missing_rate_1", 10)
	req.Network = mdb.NetworkTon
	req.Token = "TON"

	_, err := CreateTransaction(req, nil)
	if err != constant.RateAmountErr {
		t.Fatalf("create TON without rate error = %v, want %v", err, constant.RateAmountErr)
	}
}

func TestSwitchNetworkCreatesTonSubOrderAndRawLock(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0.1,"ton":0.5}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	addr := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	wallet, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, addr.StringRaw())
	if err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_switch_ton_1", 10)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent transaction: %v", err)
	}

	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "TON",
		Network: mdb.NetworkTon,
	})
	if err != nil {
		t.Fatalf("switch to TON: %v", err)
	}
	if subResp.Network != mdb.NetworkTon || subResp.Token != "TON" {
		t.Fatalf("sub order network/token = %s/%s, want ton/TON", subResp.Network, subResp.Token)
	}
	if subResp.ReceiveAddress != wallet.Address {
		t.Fatalf("sub order receive address = %q, want %q", subResp.ReceiveAddress, wallet.Address)
	}
	if got := fmt.Sprintf("%.2f", subResp.ActualAmount); got != "5.00" {
		t.Fatalf("sub order actual amount = %s, want 5.00", got)
	}
	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, addr.StringRaw(), "TON", subResp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup TON sub-order lock: %v", err)
	}
	if tradeID != subResp.TradeId {
		t.Fatalf("TON sub-order lock trade_id = %q, want %q", tradeID, subResp.TradeId)
	}
}

func TestSwitchNetworkCompletesWaitSelectPlaceholderInPlace(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0.1}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	if _, err := data.AddWalletAddress("TWaitSelectAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}

	req := newCreateTransactionRequest("order_wait_select_switch_1", 10)
	req.Token = ""
	req.Network = ""
	parentResp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create placeholder: %v", err)
	}
	if parentResp.Status != mdb.StatusWaitSelect {
		t.Fatalf("parent response status = %d, want %d", parentResp.Status, mdb.StatusWaitSelect)
	}

	switched, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "USDT",
		Network: mdb.NetworkTron,
	})
	if err != nil {
		t.Fatalf("switch placeholder to tron/usdt: %v", err)
	}
	if switched.TradeId != parentResp.TradeId {
		t.Fatalf("switch returned trade_id = %q, want parent %q", switched.TradeId, parentResp.TradeId)
	}
	if switched.Status != mdb.StatusWaitPay {
		t.Fatalf("switch status = %d, want %d", switched.Status, mdb.StatusWaitPay)
	}
	if switched.IsSelected {
		t.Fatal("switch is_selected = true, want false")
	}
	if switched.PaymentUrl != "" {
		t.Fatalf("switch payment_url = %q, want empty for unselected chain parent", switched.PaymentUrl)
	}
	if switched.Token != "USDT" || switched.Network != mdb.NetworkTron || switched.ReceiveAddress != "TWaitSelectAddress001" {
		t.Fatalf("switch chain fields = token %q network %q address %q", switched.Token, switched.Network, switched.ReceiveAddress)
	}
	if got := fmt.Sprintf("%.2f", switched.ActualAmount); got != "1.00" {
		t.Fatalf("switch actual amount = %s, want 1.00", got)
	}

	parent, err := data.GetOrderInfoByTradeId(parentResp.TradeId)
	if err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if parent.Status != mdb.StatusWaitPay || parent.ParentTradeId != "" {
		t.Fatalf("parent status/parent_trade_id = %d/%q", parent.Status, parent.ParentTradeId)
	}
	if parent.IsSelected {
		t.Fatal("parent is_selected = true, want false")
	}
	if parent.PaymentType != mdb.PaymentTypeGmpay {
		t.Fatalf("parent payment_type = %q, want %q", parent.PaymentType, mdb.PaymentTypeGmpay)
	}
	count, err := data.CountActiveSubOrders(parent.TradeId)
	if err != nil {
		t.Fatalf("count sub orders: %v", err)
	}
	if count != 0 {
		t.Fatalf("active sub-order count = %d, want 0", count)
	}
	lockTradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, parent.ReceiveAddress, parent.Token, parent.ActualAmount)
	if err != nil {
		t.Fatalf("lookup parent lock: %v", err)
	}
	if lockTradeID != parent.TradeId {
		t.Fatalf("parent lock trade_id = %q, want %q", lockTradeID, parent.TradeId)
	}
}

func TestSwitchNetworkAfterWaitSelectCompletionUsesExistingSubOrderFlow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0.1}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	if _, err := data.AddWalletAddress("TWaitSelectAddress002"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455668"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}

	req := newCreateTransactionRequest("order_wait_select_reswitch_1", 10)
	req.Token = ""
	req.Network = ""
	parentResp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create placeholder: %v", err)
	}
	first, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "USDT",
		Network: mdb.NetworkTron,
	})
	if err != nil {
		t.Fatalf("first switch: %v", err)
	}

	second, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "USDT",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("second switch: %v", err)
	}
	if second.TradeId == first.TradeId {
		t.Fatalf("second switch trade_id = parent %q, want sub-order", second.TradeId)
	}
	if second.Network != mdb.NetworkEthereum || second.Token != "USDT" {
		t.Fatalf("second switch token/network = %s/%s", second.Token, second.Network)
	}
	count, err := data.CountActiveSubOrders(parentResp.TradeId)
	if err != nil {
		t.Fatalf("count active sub orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("active sub-order count = %d, want 1", count)
	}

	_, err = SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "USDT",
		Network: mdb.NetworkBsc,
	})
	if err != constant.SubOrderLimitExceeded {
		t.Fatalf("third switch error = %v, want %v", err, constant.SubOrderLimitExceeded)
	}
}

func TestSwitchNetworkRejectsUnsupportedTonToken(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0.1}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	if _, err := data.AddWalletAddress("TTestTronAddress002"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	parentResp, err := CreateTransaction(newCreateTransactionRequest("order_switch_gram_1", 10), nil)
	if err != nil {
		t.Fatalf("create parent transaction: %v", err)
	}

	_, err = SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "GRAM",
		Network: mdb.NetworkTon,
	})
	if err != constant.SupportedAssetNotFound {
		t.Fatalf("switch unsupported TON token error = %v, want %v", err, constant.SupportedAssetNotFound)
	}
}

func TestTryProcessTonTransferMarksOrderPaidAndReleasesRawLock(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"ton":0.5}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	addr := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, addr.StringRaw()); err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}
	req := newCreateTransactionRequest("order_ton_process_1", 10)
	req.Network = mdb.NetworkTon
	req.Token = "TON"
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create TON transaction: %v", err)
	}

	transfer := &TonObservedTransfer{
		ReceiveAddress: resp.ReceiveAddress,
		Token:          mdb.ChainToken{BaseModel: mdb.BaseModel{ID: 1}, Network: mdb.NetworkTon, Symbol: "TON", Decimals: 9, Enabled: true},
		RawAmount:      big.NewInt(5_000_000_000),
		Amount:         resp.ActualAmount,
		BlockTimeMs:    time.Now().Add(time.Second).UnixMilli(),
		LT:             100,
		TxHashHex:      strings.Repeat("1", 64),
		BlockID:        TonCanonicalBlockTransactionID(addr.StringRaw(), 100, strings.Repeat("1", 64)),
	}
	TryProcessTonTransfer(transfer)

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if order.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status = %d, want %d", order.Status, mdb.StatusPaySuccess)
	}
	if order.BlockTransactionId != transfer.BlockID {
		t.Fatalf("block transaction id = %q, want %q", order.BlockTransactionId, transfer.BlockID)
	}
	lock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, addr.StringRaw(), "TON", resp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup ton lock: %v", err)
	}
	if lock != "" {
		t.Fatalf("TON lock still exists after payment: %s", lock)
	}
}

func TestTryProcessTonJettonTransferMarksUSDTOrderPaid(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0.1}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	addr := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, addr.StringRaw()); err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}
	req := newCreateTransactionRequest("order_ton_usdt_process_1", 10)
	req.Network = mdb.NetworkTon
	req.Token = "USDT"
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create TON USDT transaction: %v", err)
	}

	transfer := &TonObservedTransfer{
		ReceiveAddress: resp.ReceiveAddress,
		Token:          mdb.ChainToken{BaseModel: mdb.BaseModel{ID: 2}, Network: mdb.NetworkTon, Symbol: "USDT", Decimals: 6, Enabled: true},
		RawAmount:      big.NewInt(1_000_000),
		Amount:         resp.ActualAmount,
		BlockTimeMs:    time.Now().Add(time.Second).UnixMilli(),
		LT:             110,
		TxHashHex:      strings.Repeat("4", 64),
		BlockID:        TonCanonicalBlockTransactionID(addr.StringRaw(), 110, strings.Repeat("4", 64)),
	}
	TryProcessTonTransfer(transfer)

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if order.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status = %d, want %d", order.Status, mdb.StatusPaySuccess)
	}
	if order.BlockTransactionId != transfer.BlockID {
		t.Fatalf("block transaction id = %q, want %q", order.BlockTransactionId, transfer.BlockID)
	}
	lock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, addr.StringRaw(), "USDT", resp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup TON USDT lock: %v", err)
	}
	if lock != "" {
		t.Fatalf("TON USDT lock still exists after payment: %s", lock)
	}
}

func TestTryProcessTonTransferSkipsTransfersBeforeOrderCreation(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"ton":0.5}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	addr := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, addr.StringRaw()); err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}
	req := newCreateTransactionRequest("order_ton_old_transfer_1", 10)
	req.Network = mdb.NetworkTon
	req.Token = "TON"
	resp, err := CreateTransaction(req, nil)
	if err != nil {
		t.Fatalf("create TON transaction: %v", err)
	}
	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}

	TryProcessTonTransfer(&TonObservedTransfer{
		ReceiveAddress: resp.ReceiveAddress,
		Token:          mdb.ChainToken{BaseModel: mdb.BaseModel{ID: 1}, Network: mdb.NetworkTon, Symbol: "TON", Decimals: 9, Enabled: true},
		RawAmount:      big.NewInt(5_000_000_000),
		Amount:         resp.ActualAmount,
		BlockTimeMs:    order.CreatedAt.TimestampMilli() - 1,
		LT:             101,
		TxHashHex:      strings.Repeat("2", 64),
		BlockID:        TonCanonicalBlockTransactionID(addr.StringRaw(), 101, strings.Repeat("2", 64)),
	})

	order, err = data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if order.Status != mdb.StatusWaitPay {
		t.Fatalf("order status = %d, want wait-pay", order.Status)
	}
	if order.BlockTransactionId != "" {
		t.Fatalf("old transfer set block transaction id = %q", order.BlockTransactionId)
	}
}

func TestTryProcessTonSubOrderPaysParentAndReleasesLocks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0.1,"ton":0.5}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	if _, err := data.AddWalletAddress("TTestTronAddressForTonSub"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	addr := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, addr.StringRaw()); err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_ton_sub_parent_1", 10)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}
	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "TON",
		Network: mdb.NetworkTon,
	})
	if err != nil {
		t.Fatalf("switch to TON: %v", err)
	}

	TryProcessTonTransfer(&TonObservedTransfer{
		ReceiveAddress: subResp.ReceiveAddress,
		Token:          mdb.ChainToken{BaseModel: mdb.BaseModel{ID: 1}, Network: mdb.NetworkTon, Symbol: "TON", Decimals: 9, Enabled: true},
		RawAmount:      big.NewInt(5_000_000_000),
		Amount:         subResp.ActualAmount,
		BlockTimeMs:    time.Now().Add(time.Second).UnixMilli(),
		LT:             102,
		TxHashHex:      strings.Repeat("3", 64),
		BlockID:        TonCanonicalBlockTransactionID(addr.StringRaw(), 102, strings.Repeat("3", 64)),
	})

	parent, err := data.GetOrderInfoByTradeId(parentResp.TradeId)
	if err != nil {
		t.Fatalf("reload parent order: %v", err)
	}
	if parent.Status != mdb.StatusPaySuccess {
		t.Fatalf("parent status = %d, want paid", parent.Status)
	}
	sub, err := data.GetOrderInfoByTradeId(subResp.TradeId)
	if err != nil {
		t.Fatalf("reload sub order: %v", err)
	}
	if sub.Status != mdb.StatusPaySuccess {
		t.Fatalf("sub status = %d, want paid", sub.Status)
	}
	parentLock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, parentResp.ReceiveAddress, parentResp.Token, parentResp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup parent lock: %v", err)
	}
	if parentLock != "" {
		t.Fatalf("parent lock still exists: %s", parentLock)
	}
	subLock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, addr.StringRaw(), "TON", subResp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup sub lock: %v", err)
	}
	if subLock != "" {
		t.Fatalf("TON sub-order lock still exists: %s", subLock)
	}
}

func TestOrderProcessingMarksPaidAndReleasesLock(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     resp.ReceiveAddress,
		Token:              resp.Token,
		Network:            "tron",
		TradeId:            resp.TradeId,
		Amount:             resp.ActualAmount,
		BlockTransactionId: "block_1",
	})
	if err != nil {
		t.Fatalf("order processing: %v", err)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("get order by trade id: %v", err)
	}
	if order.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status = %d, want %d", order.Status, mdb.StatusPaySuccess)
	}
	if order.CallBackConfirm != mdb.CallBackConfirmNo {
		t.Fatalf("callback confirm = %d, want %d", order.CallBackConfirm, mdb.CallBackConfirmNo)
	}
	if order.BlockTransactionId != "block_1" {
		t.Fatalf("block transaction id = %s, want block_1", order.BlockTransactionId)
	}

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken("tron", resp.ReceiveAddress, resp.Token, resp.ActualAmount)
	if err != nil {
		t.Fatalf("get runtime lock after processing: %v", err)
	}
	if tradeID != "" {
		t.Fatalf("runtime lock still exists: %s", tradeID)
	}
}

func TestOrderProcessingRejectsDuplicateBlockForSameOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     resp.ReceiveAddress,
		Token:              resp.Token,
		Network:            "tron",
		TradeId:            resp.TradeId,
		Amount:             resp.ActualAmount,
		BlockTransactionId: "block_1",
	}
	if err = OrderProcessing(req); err != nil {
		t.Fatalf("first order processing: %v", err)
	}

	err = OrderProcessing(req)
	if err != constant.OrderBlockAlreadyProcess {
		t.Fatalf("second order processing error = %v, want %v", err, constant.OrderBlockAlreadyProcess)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload order after duplicate block: %v", err)
	}
	if order.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status after duplicate block = %d, want %d", order.Status, mdb.StatusPaySuccess)
	}
	if order.BlockTransactionId != "block_1" {
		t.Fatalf("order block transaction id after duplicate block = %s, want block_1", order.BlockTransactionId)
	}
}

func TestOrderProcessingDoesNotReviveExpiredOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	if err = dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", resp.TradeId).
		Update("status", mdb.StatusExpired).Error; err != nil {
		t.Fatalf("force order expired: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     resp.ReceiveAddress,
		Token:              resp.Token,
		Network:            "tron",
		TradeId:            resp.TradeId,
		Amount:             resp.ActualAmount,
		BlockTransactionId: "block_expired",
	})
	if err != constant.OrderStatusConflict {
		t.Fatalf("order processing error = %v, want %v", err, constant.OrderStatusConflict)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload expired order: %v", err)
	}
	if order.Status != mdb.StatusExpired {
		t.Fatalf("expired order status = %d, want %d", order.Status, mdb.StatusExpired)
	}
	if order.BlockTransactionId != "" {
		t.Fatalf("expired order block transaction id = %s, want empty", order.BlockTransactionId)
	}
}

func TestOrderProcessingOnlyOneOrderClaimsABlockTransaction(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("wallet_1"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}
	if _, err := data.AddWalletAddress("wallet_2"); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp1, err := CreateTransaction(newCreateTransactionRequest("order_1", 1), nil)
	if err != nil {
		t.Fatalf("create first transaction: %v", err)
	}
	resp2, err := CreateTransaction(newCreateTransactionRequest("order_2", 2), nil)
	if err != nil {
		t.Fatalf("create second transaction: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, tc := range []struct {
		address string
		token   string
		tradeID string
		amount  float64
	}{
		{address: resp1.ReceiveAddress, token: resp1.Token, tradeID: resp1.TradeId, amount: resp1.ActualAmount},
		{address: resp2.ReceiveAddress, token: resp2.Token, tradeID: resp2.TradeId, amount: resp2.ActualAmount},
	} {
		wg.Add(1)
		go func(address, token, tradeID string, amount float64) {
			defer wg.Done()
			<-start
			errs <- OrderProcessing(&request.OrderProcessingRequest{
				ReceiveAddress:     address,
				Token:              token,
				Network:            "tron",
				TradeId:            tradeID,
				Amount:             amount,
				BlockTransactionId: "shared_block",
			})
		}(tc.address, tc.token, tc.tradeID, tc.amount)
	}

	close(start)
	wg.Wait()
	close(errs)

	var successCount int
	var duplicateCount int
	for err := range errs {
		switch err {
		case nil:
			successCount++
		case constant.OrderBlockAlreadyProcess:
			duplicateCount++
		default:
			t.Fatalf("unexpected order processing error: %v", err)
		}
	}
	if successCount != 1 || duplicateCount != 1 {
		t.Fatalf("success=%d duplicate=%d, want 1 and 1", successCount, duplicateCount)
	}

	orders := []struct {
		tradeID string
	}{
		{tradeID: resp1.TradeId},
		{tradeID: resp2.TradeId},
	}
	var paidCount int
	var pendingCount int
	for _, item := range orders {
		order, err := data.GetOrderInfoByTradeId(item.tradeID)
		if err != nil {
			t.Fatalf("reload order %s: %v", item.tradeID, err)
		}
		switch order.Status {
		case mdb.StatusPaySuccess:
			paidCount++
			if order.BlockTransactionId != "shared_block" {
				t.Fatalf("paid order block transaction id = %s, want shared_block", order.BlockTransactionId)
			}
		case mdb.StatusWaitPay:
			pendingCount++
			if order.BlockTransactionId != "" {
				t.Fatalf("pending order block transaction id = %s, want empty", order.BlockTransactionId)
			}
		default:
			t.Fatalf("unexpected order status for %s: %d", item.tradeID, order.Status)
		}
	}
	if paidCount != 1 || pendingCount != 1 {
		t.Fatalf("paid=%d pending=%d, want 1 and 1", paidCount, pendingCount)
	}
}

func TestOrderProcessingSubOrderReturnsErrorWhenParentNotWaitPay(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455667"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_parent_for_sub", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("switch network create sub-order: %v", err)
	}

	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", parentResp.TradeId).
		Update("status", mdb.StatusExpired).Error; err != nil {
		t.Fatalf("force parent expired: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subResp.ReceiveAddress,
		Token:              strings.ToUpper(subResp.Token),
		Network:            strings.ToLower(subResp.Network),
		TradeId:            subResp.TradeId,
		Amount:             subResp.ActualAmount,
		BlockTransactionId: "block_sub_parent_not_wait",
	})
	if err == nil {
		t.Fatal("expected error when parent order is not wait-pay")
	}
}

func TestOrderProcessingSubOrderReturnsErrorWhenParentMissing(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455667"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_parent_missing", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("switch network create sub-order: %v", err)
	}

	if err := dao.Mdb.Where("trade_id = ?", parentResp.TradeId).Delete(&mdb.Orders{}).Error; err != nil {
		t.Fatalf("delete parent order: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subResp.ReceiveAddress,
		Token:              strings.ToUpper(subResp.Token),
		Network:            strings.ToLower(subResp.Network),
		TradeId:            subResp.TradeId,
		Amount:             subResp.ActualAmount,
		BlockTransactionId: "block_sub_parent_missing",
	})
	if err == nil {
		t.Fatal("expected error when parent order is missing")
	}
}

// TestOrderProcessingSubOrderPaidParentKeepsOwnFields verifies the new behavior:
// when a sub-order is paid, the parent order is marked as paid but its own
// block_transaction_id, actual_amount, and receive_address are NOT overwritten.
// The sub-order's primary-key ID is recorded in the parent's pay_by_sub_id field.
// Also verifies: parent callback_confirm=No (callback queued), sub-order callback_confirm=Ok.
func TestOrderProcessingSubOrderPaidParentKeepsOwnFields(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455667"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_sub_pay_test", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	// Snapshot the parent's original fields before any payment.
	originalParent, err := data.GetOrderInfoByTradeId(parentResp.TradeId)
	if err != nil {
		t.Fatalf("load original parent: %v", err)
	}

	subResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("switch network create sub-order: %v", err)
	}

	// Load the sub-order to get its DB primary-key ID.
	subOrder, err := data.GetOrderInfoByTradeId(subResp.TradeId)
	if err != nil {
		t.Fatalf("load sub-order: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subResp.ReceiveAddress,
		Token:              strings.ToUpper(subResp.Token),
		Network:            strings.ToLower(subResp.Network),
		TradeId:            subResp.TradeId,
		Amount:             subResp.ActualAmount,
		BlockTransactionId: "block_sub_paid",
	})
	if err != nil {
		t.Fatalf("order processing sub-order: %v", err)
	}

	// Sub-order must be paid with the block hash and no pending callback.
	sub, err := data.GetOrderInfoByTradeId(subResp.TradeId)
	if err != nil {
		t.Fatalf("reload sub-order: %v", err)
	}
	if sub.Status != mdb.StatusPaySuccess {
		t.Fatalf("sub-order status = %d, want %d", sub.Status, mdb.StatusPaySuccess)
	}
	if sub.BlockTransactionId != "block_sub_paid" {
		t.Fatalf("sub-order block_transaction_id = %q, want %q", sub.BlockTransactionId, "block_sub_paid")
	}
	if sub.CallBackConfirm != mdb.CallBackConfirmOk {
		t.Fatalf("sub-order callback_confirm = %d, want %d (no callback for sub-order)", sub.CallBackConfirm, mdb.CallBackConfirmOk)
	}

	// Parent must be paid but its own fields must be unchanged.
	parent, err := data.GetOrderInfoByTradeId(parentResp.TradeId)
	if err != nil {
		t.Fatalf("reload parent order: %v", err)
	}
	if parent.Status != mdb.StatusPaySuccess {
		t.Fatalf("parent status = %d, want %d", parent.Status, mdb.StatusPaySuccess)
	}
	if parent.BlockTransactionId != "" {
		t.Fatalf("parent block_transaction_id = %q, want empty (parent was not directly paid)", parent.BlockTransactionId)
	}
	if parent.ReceiveAddress != originalParent.ReceiveAddress {
		t.Fatalf("parent receive_address changed: got %q, want %q", parent.ReceiveAddress, originalParent.ReceiveAddress)
	}
	if parent.ActualAmount != originalParent.ActualAmount {
		t.Fatalf("parent actual_amount changed: got %v, want %v", parent.ActualAmount, originalParent.ActualAmount)
	}
	if parent.PayBySubId != subOrder.ID {
		t.Fatalf("parent pay_by_sub_id = %d, want %d (sub-order ID)", parent.PayBySubId, subOrder.ID)
	}
	if parent.CallBackConfirm != mdb.CallBackConfirmNo {
		t.Fatalf("parent callback_confirm = %d, want %d (callback must be queued)", parent.CallBackConfirm, mdb.CallBackConfirmNo)
	}
}

func TestOrderProcessingParentDirectPayExpiresOkPayProviderOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_parent_direct_okpay_expire", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	subOrder := &mdb.Orders{
		TradeId:         "okpay_sub_parent_direct_expire",
		OrderId:         "okpay_sub_parent_direct_expire",
		ParentTradeId:   parentResp.TradeId,
		Amount:          parentResp.Amount,
		Currency:        parentResp.Currency,
		ActualAmount:    0.15,
		ReceiveAddress:  "OKPAY",
		Token:           "USDT",
		Network:         mdb.NetworkTron,
		Status:          mdb.StatusWaitPay,
		NotifyUrl:       "",
		CallBackConfirm: mdb.CallBackConfirmOk,
		PayProvider:     mdb.PaymentProviderOkPay,
	}
	if err := dao.Mdb.Create(subOrder).Error; err != nil {
		t.Fatalf("create okpay sub-order: %v", err)
	}
	providerRow := &mdb.ProviderOrder{
		TradeId:         subOrder.TradeId,
		Provider:        mdb.PaymentProviderOkPay,
		ProviderOrderID: "okp-parent-direct-expire",
		PayURL:          "https://t.me/ExampleWalletBot?start=shop_deposit--okpay-order-parent-direct-expire",
		Amount:          subOrder.ActualAmount,
		Coin:            subOrder.Token,
		Status:          mdb.ProviderOrderStatusPending,
	}
	if err := dao.Mdb.Create(providerRow).Error; err != nil {
		t.Fatalf("create provider row: %v", err)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     parentResp.ReceiveAddress,
		Token:              strings.ToUpper(parentResp.Token),
		Network:            mdb.NetworkTron,
		TradeId:            parentResp.TradeId,
		Amount:             parentResp.ActualAmount,
		BlockTransactionId: "block_parent_direct_okpay_expire",
	})
	if err != nil {
		t.Fatalf("order processing parent direct pay: %v", err)
	}

	expiredSub, err := data.GetOrderInfoByTradeId(subOrder.TradeId)
	if err != nil {
		t.Fatalf("reload sub-order: %v", err)
	}
	if expiredSub.Status != mdb.StatusExpired {
		t.Fatalf("sub-order status = %d, want %d", expiredSub.Status, mdb.StatusExpired)
	}

	expiredProviderRow, err := data.GetProviderOrderByTradeIDAndProvider(subOrder.TradeId, mdb.PaymentProviderOkPay)
	if err != nil {
		t.Fatalf("reload provider row: %v", err)
	}
	if expiredProviderRow.Status != mdb.ProviderOrderStatusExpired {
		t.Fatalf("provider row status = %q, want %q", expiredProviderRow.Status, mdb.ProviderOrderStatusExpired)
	}
}

// TestOrderProcessingSubOrderExpiresSiblingsAndReleasesLocks verifies that when one
// sub-order is paid all sibling sub-orders are expired and their runtime locks (as
// well as the parent's lock) are released.
func TestOrderProcessingSubOrderExpiresSiblingsAndReleasesLocks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress("TTestTronAddress001"); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0xA1B2c3D4e5F60718293aBcDeF001122334455667"); err != nil {
		t.Fatalf("add ethereum wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkBsc, "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"); err != nil {
		t.Fatalf("add bsc wallet: %v", err)
	}

	parentReq := newCreateTransactionRequest("order_sib_expiry_test", 1)
	parentReq.Network = mdb.NetworkTron
	parentResp, err := CreateTransaction(parentReq, nil)
	if err != nil {
		t.Fatalf("create parent order: %v", err)
	}

	subEthResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parentResp.TradeId,
		Token:   "usdt",
		Network: mdb.NetworkEthereum,
	})
	if err != nil {
		t.Fatalf("switch to ethereum sub-order: %v", err)
	}

	bscTradeID := GenerateCode()
	bscAddress := strings.ToLower("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	subBsc := &mdb.Orders{
		TradeId:         bscTradeID,
		OrderId:         bscTradeID,
		ParentTradeId:   parentResp.TradeId,
		Amount:          parentResp.Amount,
		Currency:        parentResp.Currency,
		ActualAmount:    subEthResp.ActualAmount,
		ReceiveAddress:  bscAddress,
		Token:           "USDT",
		Network:         mdb.NetworkBsc,
		Status:          mdb.StatusWaitPay,
		IsSelected:      true,
		CallBackConfirm: mdb.CallBackConfirmOk,
		PaymentType:     mdb.PaymentTypeGmpay,
		PayProvider:     mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(subBsc).Error; err != nil {
		t.Fatalf("seed bsc sibling sub-order: %v", err)
	}
	if err := data.LockTransaction(mdb.NetworkBsc, bscAddress, "USDT", bscTradeID, subBsc.ActualAmount, time.Hour); err != nil {
		t.Fatalf("lock bsc sibling amount: %v", err)
	}

	// Pay the Ethereum sub-order.
	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     subEthResp.ReceiveAddress,
		Token:              strings.ToUpper(subEthResp.Token),
		Network:            strings.ToLower(subEthResp.Network),
		TradeId:            subEthResp.TradeId,
		Amount:             subEthResp.ActualAmount,
		BlockTransactionId: "block_sib_eth",
	})
	if err != nil {
		t.Fatalf("order processing eth sub-order: %v", err)
	}

	// BSC sibling must be expired.
	reloadedBsc, err := data.GetOrderInfoByTradeId(subBsc.TradeId)
	if err != nil {
		t.Fatalf("reload bsc sub-order: %v", err)
	}
	if reloadedBsc.Status != mdb.StatusExpired {
		t.Fatalf("bsc sibling status = %d, want %d (expired)", reloadedBsc.Status, mdb.StatusExpired)
	}

	// Parent runtime lock must be released.
	parentLock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(
		mdb.NetworkTron, parentResp.ReceiveAddress, parentResp.Token, parentResp.ActualAmount)
	if err != nil {
		t.Fatalf("check parent runtime lock: %v", err)
	}
	if parentLock != "" {
		t.Fatalf("parent runtime lock still held: trade_id=%s", parentLock)
	}

	// BSC sibling runtime lock must be released.
	sibLock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(
		mdb.NetworkBsc, subBsc.ReceiveAddress, subBsc.Token, subBsc.ActualAmount)
	if err != nil {
		t.Fatalf("check bsc sibling runtime lock: %v", err)
	}
	if sibLock != "" {
		t.Fatalf("bsc sibling runtime lock still held: trade_id=%s", sibLock)
	}

	// Ethereum sub-order runtime lock must also be released.
	ethLock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(
		mdb.NetworkEthereum, subEthResp.ReceiveAddress, subEthResp.Token, subEthResp.ActualAmount)
	if err != nil {
		t.Fatalf("check eth sub-order runtime lock: %v", err)
	}
	if ethLock != "" {
		t.Fatalf("eth sub-order runtime lock still held: trade_id=%s", ethLock)
	}
}
