package service

import (
	"testing"
	"time"

	"github.com/assimon/luuu/internal/testutil"
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/request"
	"github.com/assimon/luuu/model/response"
)

const testServiceSolAddr1 = "So11111111111111111111111111111111111111112"

func hasPaymentOption(options []response.CheckoutPaymentOption, network, token string) bool {
	for _, option := range options {
		if option.Network == network && option.Token == token {
			return true
		}
	}
	return false
}

func TestGetCheckoutCounterByTradeIdIncludesCurrentAndAvailableOptions(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkSolana, testServiceSolAddr1); err != nil {
		t.Fatalf("add solana wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("checkout_options", 1))
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	checkout, err := GetCheckoutCounterByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("get checkout counter: %v", err)
	}
	if !hasPaymentOption(checkout.PaymentOptions, mdb.NetworkTron, "USDT") {
		t.Fatalf("missing current tron option: %+v", checkout.PaymentOptions)
	}
	if !hasPaymentOption(checkout.PaymentOptions, mdb.NetworkSolana, "USDT") {
		t.Fatalf("missing available solana option: %+v", checkout.PaymentOptions)
	}
}

func TestGetCheckoutCounterByTradeIdSkipsRateUnavailableOptions(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkSolana, testServiceSolAddr1); err != nil {
		t.Fatalf("add solana wallet: %v", err)
	}

	if err := dao.Mdb.Create(&mdb.SupportedAsset{
		Network: mdb.NetworkSolana,
		Token:   "USDC",
		Status:  mdb.TokenStatusEnable,
	}).Error; err != nil {
		t.Fatalf("create extra supported asset: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("checkout_rate_filter", 1))
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	checkout, err := GetCheckoutCounterByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("get checkout counter: %v", err)
	}
	if hasPaymentOption(checkout.PaymentOptions, mdb.NetworkSolana, "USDC") {
		t.Fatalf("unexpected rate-unavailable option: %+v", checkout.PaymentOptions)
	}
}

func TestGetCheckoutCounterByTradeIdKeepsCurrentOrderOptionAfterDisable(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("checkout_current_option", 1))
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	if err = dao.Mdb.Model(&mdb.SupportedAsset{}).
		Where("network = ? AND token = ?", mdb.NetworkTron, "USDT").
		Update("status", mdb.TokenStatusDisable).Error; err != nil {
		t.Fatalf("disable supported asset: %v", err)
	}

	checkout, err := GetCheckoutCounterByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("get checkout counter: %v", err)
	}
	if !hasPaymentOption(checkout.PaymentOptions, mdb.NetworkTron, "USDT") {
		t.Fatalf("missing current order option after disable: %+v", checkout.PaymentOptions)
	}
}

func TestGetCheckoutCounterByTradeIdReturnsSelectedSubOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkSolana, testServiceSolAddr1); err != nil {
		t.Fatalf("add solana wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("checkout_selected_sub", 1))
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	switchResp, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: resp.TradeId,
		Token:   "usdt",
		Network: "solana",
	})
	if err != nil {
		t.Fatalf("switch network: %v", err)
	}

	checkout, err := GetCheckoutCounterByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("get checkout counter: %v", err)
	}
	if checkout.TradeId != switchResp.TradeId {
		t.Fatalf("expected selected sub-order %s, got %s", switchResp.TradeId, checkout.TradeId)
	}
	if checkout.Network != mdb.NetworkSolana || checkout.Token != "USDT" {
		t.Fatalf("unexpected selected checkout payload: %+v", checkout)
	}
}

func TestSwitchNetworkRefreshesFamilyExpirationAndLocks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkSolana, testServiceSolAddr1); err != nil {
		t.Fatalf("add solana wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("checkout_refresh_family", 1))
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	firstSwitch, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: resp.TradeId,
		Token:   "usdt",
		Network: "solana",
	})
	if err != nil {
		t.Fatalf("switch network: %v", err)
	}

	parentBefore, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("load parent order: %v", err)
	}
	subBefore, err := data.GetOrderInfoByTradeId(firstSwitch.TradeId)
	if err != nil {
		t.Fatalf("load sub-order: %v", err)
	}
	parentLockBefore := new(mdb.TransactionLock)
	if err = dao.RuntimeDB.Where("trade_id = ?", resp.TradeId).Limit(1).Find(parentLockBefore).Error; err != nil {
		t.Fatalf("load parent lock: %v", err)
	}
	subLockBefore := new(mdb.TransactionLock)
	if err = dao.RuntimeDB.Where("trade_id = ?", firstSwitch.TradeId).Limit(1).Find(subLockBefore).Error; err != nil {
		t.Fatalf("load sub lock: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	secondSwitch, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: resp.TradeId,
		Token:   "usdt",
		Network: "solana",
	})
	if err != nil {
		t.Fatalf("switch network second time: %v", err)
	}
	if secondSwitch.TradeId != firstSwitch.TradeId {
		t.Fatalf("expected existing sub-order %s, got %s", firstSwitch.TradeId, secondSwitch.TradeId)
	}

	parentAfter, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload parent order: %v", err)
	}
	subAfter, err := data.GetOrderInfoByTradeId(firstSwitch.TradeId)
	if err != nil {
		t.Fatalf("reload sub-order: %v", err)
	}
	parentLockAfter := new(mdb.TransactionLock)
	if err = dao.RuntimeDB.Where("trade_id = ?", resp.TradeId).Limit(1).Find(parentLockAfter).Error; err != nil {
		t.Fatalf("reload parent lock: %v", err)
	}
	subLockAfter := new(mdb.TransactionLock)
	if err = dao.RuntimeDB.Where("trade_id = ?", firstSwitch.TradeId).Limit(1).Find(subLockAfter).Error; err != nil {
		t.Fatalf("reload sub lock: %v", err)
	}

	if parentAfter.CreatedAt.TimestampMilli() <= parentBefore.CreatedAt.TimestampMilli() {
		t.Fatalf("expected parent created_at to refresh: before=%v after=%v", parentBefore.CreatedAt, parentAfter.CreatedAt)
	}
	if subAfter.CreatedAt.TimestampMilli() <= subBefore.CreatedAt.TimestampMilli() {
		t.Fatalf("expected sub-order created_at to refresh: before=%v after=%v", subBefore.CreatedAt, subAfter.CreatedAt)
	}
	if !parentLockAfter.ExpiresAt.After(parentLockBefore.ExpiresAt) {
		t.Fatalf("expected parent lock to refresh: before=%v after=%v", parentLockBefore.ExpiresAt, parentLockAfter.ExpiresAt)
	}
	if !subLockAfter.ExpiresAt.After(subLockBefore.ExpiresAt) {
		t.Fatalf("expected sub lock to refresh: before=%v after=%v", subLockBefore.ExpiresAt, subLockAfter.ExpiresAt)
	}
}

func TestGetCheckoutCounterByTradeIdReturnsTerminalOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("checkout_terminal", 1))
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if err = dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", resp.TradeId).
		Update("status", mdb.StatusExpired).Error; err != nil {
		t.Fatalf("expire order: %v", err)
	}

	checkout, err := GetCheckoutCounterByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("get checkout counter: %v", err)
	}
	if checkout.Status != mdb.StatusExpired {
		t.Fatalf("expected expired checkout status, got %+v", checkout)
	}
	if len(checkout.PaymentOptions) != 0 {
		t.Fatalf("expected no payment options for terminal order, got %+v", checkout.PaymentOptions)
	}
}
