package service

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/notify"
	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm/clause"
)

func upsertTaskServiceTestChainToken(t *testing.T, token mdb.ChainToken) {
	t.Helper()
	if err := dao.Mdb.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "network"}, {Name: "symbol"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"contract_address",
			"decimals",
			"enabled",
			"min_amount",
		}),
	}).Create(&token).Error; err != nil {
		t.Fatalf("seed chain token: %v", err)
	}
}

func TestSendPaymentNotificationUsesLatestOrderUpdatedAt(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const channelType = "test-pay-success-time"
	got := make(chan string, 1)
	notify.RegisterSender(channelType, func(config, text string) error {
		got <- text
		return nil
	})

	if err := dao.Mdb.Create(&mdb.NotificationChannel{
		Type:    channelType,
		Name:    "test",
		Config:  "{}",
		Events:  `{"pay_success":true}`,
		Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed notification channel: %v", err)
	}

	order := &mdb.Orders{
		TradeId:        "T202604270001",
		OrderId:        "ORD202604270001",
		Amount:         100,
		Currency:       "cny",
		ActualAmount:   14.28,
		Token:          "USDT",
		Network:        mdb.NetworkTron,
		ReceiveAddress: "TTestAddress",
		Status:         mdb.StatusWaitPay,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}

	const createdAt = "2026-04-27 09:00:00"
	const staleUpdatedAt = "2026-04-27 09:01:00"
	const paidAt = "2026-04-27 10:20:30"
	if err := dao.Mdb.Exec("UPDATE orders SET created_at = ?, updated_at = ? WHERE trade_id = ?", createdAt, staleUpdatedAt, order.TradeId).Error; err != nil {
		t.Fatalf("set initial timestamps: %v", err)
	}

	var staleOrderModel mdb.Orders
	if err := dao.Mdb.Where("trade_id = ?", order.TradeId).Take(&staleOrderModel).Error; err != nil {
		t.Fatalf("load stale order: %v", err)
	}

	if err := dao.Mdb.Exec("UPDATE orders SET status = ?, updated_at = ? WHERE trade_id = ?", mdb.StatusPaySuccess, paidAt, order.TradeId).Error; err != nil {
		t.Fatalf("set paid timestamp: %v", err)
	}

	sendPaymentNotification(&staleOrderModel)

	select {
	case text := <-got:
		if !strings.Contains(text, "支付时间："+paidAt) {
			t.Fatalf("notification payment time = %q, want %s", text, paidAt)
		}
		if strings.Contains(text, "支付时间："+staleUpdatedAt) {
			t.Fatalf("notification used stale payment time: %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

func TestTryProcessEvmERC20TransferUsesChainTokenContract(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const (
		tradeID  = "T202605110001"
		orderID  = "ORD202605110001"
		tokenSym = "FOO"
		amount   = 12.34
	)
	contract := common.HexToAddress("0x1111111111111111111111111111111111111111")
	receiveAddress := common.HexToAddress("0x2222222222222222222222222222222222222222")

	if err := dao.Mdb.Create(&mdb.ChainToken{
		Network:         mdb.NetworkEthereum,
		Symbol:          tokenSym,
		ContractAddress: contract.Hex(),
		Decimals:        8,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("seed chain token: %v", err)
	}

	order := &mdb.Orders{
		TradeId:        tradeID,
		OrderId:        orderID,
		Amount:         100,
		Currency:       "CNY",
		ActualAmount:   amount,
		Token:          tokenSym,
		Network:        mdb.NetworkEthereum,
		ReceiveAddress: strings.ToLower(receiveAddress.Hex()),
		Status:         mdb.StatusWaitPay,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := data.LockTransaction(mdb.NetworkEthereum, order.ReceiveAddress, order.Token, order.TradeId, order.ActualAmount, time.Hour); err != nil {
		t.Fatalf("lock transaction: %v", err)
	}

	rawValue := big.NewInt(1_234_000_000) // 12.34 with 8 decimals
	TryProcessEvmERC20Transfer(mdb.NetworkEthereum, contract, receiveAddress, rawValue, "0xfoo-hash", time.Now().UnixMilli())

	paid, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load paid order: %v", err)
	}
	if paid.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status = %d, want %d", paid.Status, mdb.StatusPaySuccess)
	}
	if paid.BlockTransactionId != "0xfoo-hash" {
		t.Fatalf("block transaction id = %q, want %q", paid.BlockTransactionId, "0xfoo-hash")
	}

	lockTradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkEthereum, receiveAddress.Hex(), tokenSym, amount)
	if err != nil {
		t.Fatalf("lookup released lock: %v", err)
	}
	if lockTradeID != "" {
		t.Fatalf("runtime lock still exists for trade_id=%q", lockTradeID)
	}
}

func TestTryProcessEvmERC20TransferSkipsTransfersBeforeOrderCreation(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const (
		tradeID  = "T202605110002"
		orderID  = "ORD202605110002"
		tokenSym = "USDT"
		amount   = 25.5
	)
	contract := common.HexToAddress("0x3333333333333333333333333333333333333333")
	receiveAddress := common.HexToAddress("0x4444444444444444444444444444444444444444")

	upsertTaskServiceTestChainToken(t, mdb.ChainToken{
		Network:         mdb.NetworkEthereum,
		Symbol:          tokenSym,
		ContractAddress: contract.Hex(),
		Decimals:        6,
		Enabled:         true,
	})

	order := &mdb.Orders{
		TradeId:        tradeID,
		OrderId:        orderID,
		Amount:         100,
		Currency:       "CNY",
		ActualAmount:   amount,
		Token:          tokenSym,
		Network:        mdb.NetworkEthereum,
		ReceiveAddress: strings.ToLower(receiveAddress.Hex()),
		Status:         mdb.StatusWaitPay,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := data.LockTransaction(mdb.NetworkEthereum, order.ReceiveAddress, order.Token, order.TradeId, order.ActualAmount, time.Hour); err != nil {
		t.Fatalf("lock transaction: %v", err)
	}

	rawValue := big.NewInt(25_500_000) // 25.5 with 6 decimals
	oldBlockTsMs := order.CreatedAt.TimestampMilli() - 1
	TryProcessEvmERC20Transfer(mdb.NetworkEthereum, contract, receiveAddress, rawValue, "0xold-hash", oldBlockTsMs)

	got, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}
	if got.Status != mdb.StatusWaitPay {
		t.Fatalf("order status = %d, want %d", got.Status, mdb.StatusWaitPay)
	}
	if got.BlockTransactionId != "" {
		t.Fatalf("block transaction id = %q, want empty", got.BlockTransactionId)
	}

	lockTradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkEthereum, receiveAddress.Hex(), tokenSym, amount)
	if err != nil {
		t.Fatalf("lookup retained lock: %v", err)
	}
	if lockTradeID != tradeID {
		t.Fatalf("runtime lock trade_id = %q, want %q", lockTradeID, tradeID)
	}
}

func TestTryProcessEvmERC20TransferRecoversExpiredOrderAfterLockCleanup(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const (
		tradeID  = "T202605110003"
		orderID  = "ORD202605110003"
		tokenSym = "USDT"
		amount   = 17.68
	)
	contract := common.HexToAddress("0x5555555555555555555555555555555555555555")
	receiveAddress := common.HexToAddress("0x6666666666666666666666666666666666666666")

	upsertTaskServiceTestChainToken(t, mdb.ChainToken{
		Network:         mdb.NetworkBsc,
		Symbol:          tokenSym,
		ContractAddress: contract.Hex(),
		Decimals:        6,
		Enabled:         true,
	})

	order := &mdb.Orders{
		TradeId:        tradeID,
		OrderId:        orderID,
		Amount:         100,
		Currency:       "CNY",
		ActualAmount:   amount,
		Token:          tokenSym,
		Network:        mdb.NetworkBsc,
		ReceiveAddress: strings.ToLower(receiveAddress.Hex()),
		Status:         mdb.StatusExpired,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed expired order: %v", err)
	}
	createdAt := time.Now().Add(-2 * time.Minute)
	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Update("created_at", createdAt).Error; err != nil {
		t.Fatalf("backdate order: %v", err)
	}

	rawValue := big.NewInt(17_680_000) // 17.68 with 6 decimals
	blockTsMs := createdAt.Add(time.Minute).UnixMilli()
	TryProcessEvmERC20Transfer(mdb.NetworkBsc, contract, receiveAddress, rawValue, "0xrecovered-hash", blockTsMs)

	paid, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load paid order: %v", err)
	}
	if paid.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status = %d, want %d", paid.Status, mdb.StatusPaySuccess)
	}
	if paid.BlockTransactionId != "0xrecovered-hash" {
		t.Fatalf("block transaction id = %q, want recovered hash", paid.BlockTransactionId)
	}
}

func TestTryProcessEvmERC20TransferSkipsExpiredOrderAfterPaymentWindow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const (
		tradeID  = "T202605110004"
		orderID  = "ORD202605110004"
		tokenSym = "USDT"
		amount   = 19.99
	)
	contract := common.HexToAddress("0x7777777777777777777777777777777777777777")
	receiveAddress := common.HexToAddress("0x8888888888888888888888888888888888888888")

	upsertTaskServiceTestChainToken(t, mdb.ChainToken{
		Network:         mdb.NetworkBsc,
		Symbol:          tokenSym,
		ContractAddress: contract.Hex(),
		Decimals:        6,
		Enabled:         true,
	})

	order := &mdb.Orders{
		TradeId:        tradeID,
		OrderId:        orderID,
		Amount:         100,
		Currency:       "CNY",
		ActualAmount:   amount,
		Token:          tokenSym,
		Network:        mdb.NetworkBsc,
		ReceiveAddress: strings.ToLower(receiveAddress.Hex()),
		Status:         mdb.StatusExpired,
		PayProvider:    mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatalf("seed expired order: %v", err)
	}
	createdAt := time.Now().Add(-2 * config.GetOrderExpirationTimeDuration())
	if err := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Update("created_at", createdAt).Error; err != nil {
		t.Fatalf("backdate order: %v", err)
	}

	rawValue := big.NewInt(19_990_000) // 19.99 with 6 decimals
	blockTsMs := createdAt.Add(config.GetOrderExpirationTimeDuration() + time.Minute).UnixMilli()
	TryProcessEvmERC20Transfer(mdb.NetworkBsc, contract, receiveAddress, rawValue, "0xlate-hash", blockTsMs)

	got, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		t.Fatalf("load order: %v", err)
	}
	if got.Status != mdb.StatusExpired {
		t.Fatalf("order status = %d, want %d", got.Status, mdb.StatusExpired)
	}
	if got.BlockTransactionId != "" {
		t.Fatalf("block transaction id = %q, want empty", got.BlockTransactionId)
	}
}
