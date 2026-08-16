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
	"github.com/dromara/carbon/v2"
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

func TestTryProcessEvmERC20TransferReturnsDatabaseFailure(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	sqlDB, err := dao.Mdb.DB()
	if err != nil {
		t.Fatalf("读取数据库句柄失败: %v", err)
	}
	if err = sqlDB.Close(); err != nil {
		t.Fatalf("关闭测试数据库失败: %v", err)
	}

	err = ProcessEvmERC20Transfer(
		mdb.NetworkBase,
		common.HexToAddress("0x9999999999999999999999999999999999999999"),
		common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		big.NewInt(1),
		"0xdatabase-error",
		time.Now().UnixMilli(),
	)
	if err == nil {
		t.Fatal("数据库失败时应返回错误，避免补扫游标提前推进")
	}
}

func TestProcessEvmERC20TransferRejectsMissingTokenConfiguration(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	err := ProcessEvmERC20Transfer(
		mdb.NetworkBase,
		common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc"),
		big.NewInt(1),
		"0xmissing-token",
		time.Now().UnixMilli(),
	)
	if err == nil {
		t.Fatal("补扫命中的合约配置消失时应阻止游标推进")
	}
}

func TestEvmTransferAllowedStatusesIncludesExpiredWithinPaymentWindow(t *testing.T) {
	order := &mdb.Orders{Status: mdb.StatusWaitPay}
	order.CreatedAt = *carbon.NewTime(carbon.Now())
	blockTsMs := order.CreatedAt.AddMinute().TimestampMilli()

	statuses, _, payable := evmTransferAllowedStatuses(order, blockTsMs)
	if !payable {
		t.Fatal("有效期内的转账应允许支付")
	}
	if len(statuses) != 2 || statuses[0] != mdb.StatusWaitPay || statuses[1] != mdb.StatusExpired {
		t.Fatalf("allowed statuses = %v, want [%d %d]", statuses, mdb.StatusWaitPay, mdb.StatusExpired)
	}
}

func TestProcessEvmERC20TransferResumesPaidSubOrderWithoutRuntimeLock(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const (
		parentTradeID = "T202608160100"
		subTradeID    = "T202608160101"
		blockID       = "0x1234567890abcdef"
	)
	receiveAddress := common.HexToAddress("0xdddddddddddddddddddddddddddddddddddddddd")
	parent := &mdb.Orders{
		TradeId:       parentTradeID,
		OrderId:       "ORDER-PARENT-RESUME",
		Status:        mdb.StatusWaitPay,
		PaymentType:   mdb.PaymentTypeGmpay,
		PayProvider:   mdb.PaymentProviderOnChain,
		SignAlgorithm: "md5",
	}
	if err := dao.Mdb.Create(parent).Error; err != nil {
		t.Fatalf("创建父单失败: %v", err)
	}
	sub := &mdb.Orders{
		TradeId:            subTradeID,
		OrderId:            "ORDER-SUB-RESUME",
		ParentTradeId:      parentTradeID,
		BlockTransactionId: blockID,
		ActualAmount:       1.25,
		ReceiveAddress:     strings.ToLower(receiveAddress.Hex()),
		Token:              "USDC",
		Network:            mdb.NetworkBase,
		Status:             mdb.StatusPaySuccess,
		CallBackConfirm:    mdb.CallBackConfirmNo,
		PaymentType:        mdb.PaymentTypeGmpay,
		PayProvider:        mdb.PaymentProviderOnChain,
		SignAlgorithm:      "md5",
	}
	if err := dao.Mdb.Create(sub).Error; err != nil {
		t.Fatalf("创建已提交子单失败: %v", err)
	}

	err := ProcessEvmERC20Transfer(
		mdb.NetworkBase,
		common.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"),
		receiveAddress,
		big.NewInt(1_250_000),
		blockID,
		time.Now().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("继续处理已提交子单失败: %v", err)
	}

	gotParent, err := data.GetOrderInfoByTradeId(parentTradeID)
	if err != nil {
		t.Fatalf("读取父单失败: %v", err)
	}
	gotSub, err := data.GetOrderInfoByTradeId(subTradeID)
	if err != nil {
		t.Fatalf("读取子单失败: %v", err)
	}
	if gotParent.Status != mdb.StatusPaySuccess || gotParent.PayBySubId != gotSub.ID {
		t.Fatalf("父单未完成收尾: status=%d pay_by_sub_id=%d want_sub_id=%d", gotParent.Status, gotParent.PayBySubId, gotSub.ID)
	}
	if gotSub.CallBackConfirm != mdb.CallBackConfirmOk {
		t.Fatalf("子单 callback_confirm = %d, want %d", gotSub.CallBackConfirm, mdb.CallBackConfirmOk)
	}
}
