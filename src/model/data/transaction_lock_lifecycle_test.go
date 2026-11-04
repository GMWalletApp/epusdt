package data

import (
	"errors"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/constant"
	"gorm.io/gorm"
)

func seedRenewalOrder(t *testing.T) *mdb.Orders {
	t.Helper()
	if err := ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	order := &mdb.Orders{
		TradeId: "renewal-order", OrderId: "renewal-order",
		Network: mdb.NetworkTron, Token: "USDT", ReceiveAddress: "renewal-wallet",
		ActualAmount: 1.23, Status: mdb.StatusWaitPay, PayProvider: mdb.PaymentProviderOnChain,
	}
	if err := dao.Mdb.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Model(order).UpdateColumn("created_at", time.Now().Add(-9*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := LockTransaction(order.Network, order.ReceiveAddress, order.Token, order.TradeId, order.ActualAmount, time.Minute); err != nil {
		t.Fatal(err)
	}
	order, err := GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func TestUnlockTransactionReleasesOnlyItsOwner(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	order := seedRenewalOrder(t)
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).
		Where("trade_id = ?", order.TradeId).
		UpdateColumn("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := LockTransaction(order.Network, order.ReceiveAddress, order.Token, "new-owner", order.ActualAmount, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := UnLockTransactionByTradeId(order.TradeId); err != nil {
		t.Fatal(err)
	}
	owner, err := GetTradeIdByWalletAddressAndAmountAndToken(order.Network, order.ReceiveAddress, order.Token, order.ActualAmount)
	if err != nil || owner != "new-owner" {
		t.Fatalf("reservation owner = %q, err=%v, want new-owner", owner, err)
	}
	if err := UnLockTransactionByTradeId("new-owner"); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("remaining locks = %d, err=%v", count, err)
	}
}

func TestRefreshOrderExpirationPreservesStoredPrecision(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	order := seedRenewalOrder(t)
	var before mdb.TransactionLock
	if err := dao.RuntimeDB.Where("trade_id = ?", order.TradeId).Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "4", mdb.SettingTypeInt); err != nil {
		t.Fatal(err)
	}
	if err := RefreshOrderExpiration(order.TradeId); err != nil {
		t.Fatal(err)
	}
	var after mdb.TransactionLock
	if err := dao.RuntimeDB.Where("trade_id = ?", order.TradeId).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if before.ID != after.ID || before.AmountPrecision != after.AmountPrecision || before.AmountScaled != after.AmountScaled {
		t.Fatalf("renewal changed the existing reservation: before=%+v after=%+v", before, after)
	}
	refreshed, err := GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ExpiresAt.After(before.ExpiresAt) ||
		after.ExpiresAt.Before(refreshed.CreatedAt.StdTime().Add(10*time.Minute)) {
		t.Fatal("reservation does not cover the refreshed order")
	}
}

func TestRefreshOrderExpirationFailsClosed(t *testing.T) {
	for _, scenario := range []string{"missing", "expired", "reassigned", "wrong-amount", "paid", "closed", "timed-out"} {
		t.Run(scenario, func(t *testing.T) {
			cleanup := testutil.SetupTestDatabases(t)
			defer cleanup()
			order := seedRenewalOrder(t)
			var err error
			switch scenario {
			case "missing":
				err = UnLockTransactionByTradeId(order.TradeId)
			case "expired", "reassigned":
				err = dao.RuntimeDB.Model(&mdb.TransactionLock{}).Where("trade_id = ?", order.TradeId).
					UpdateColumn("expires_at", time.Now().Add(-time.Second)).Error
				if err == nil && scenario == "reassigned" {
					err = LockTransaction(order.Network, order.ReceiveAddress, order.Token, "new-owner", order.ActualAmount, time.Hour)
				}
			case "wrong-amount":
				err = LockTransaction(order.Network, order.ReceiveAddress, order.Token, order.TradeId, 2.34, time.Hour)
			case "paid":
				err = dao.Mdb.Model(order).UpdateColumn("status", mdb.StatusPaySuccess).Error
			case "closed":
				err = dao.Mdb.Model(order).UpdateColumn("status", mdb.StatusExpired).Error
			case "timed-out":
				err = dao.Mdb.Model(order).UpdateColumn("created_at", time.Now().Add(-11*time.Minute)).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := GetOrderInfoByTradeId(order.TradeId)
			if err != nil {
				t.Fatal(err)
			}
			if err := RefreshOrderExpiration(order.TradeId); !errors.Is(err, constant.OrderStatusConflict) {
				t.Fatalf("refresh error = %v, want status conflict", err)
			}
			after, err := GetOrderInfoByTradeId(order.TradeId)
			if err != nil {
				t.Fatal(err)
			}
			if !after.CreatedAt.StdTime().Equal(before.CreatedAt.StdTime()) || after.Status != before.Status {
				t.Fatal("failed renewal changed the order")
			}
			if scenario == "reassigned" {
				owner, err := GetTradeIdByWalletAddressAndAmountAndToken(order.Network, order.ReceiveAddress, order.Token, order.ActualAmount)
				if err != nil || owner != "new-owner" {
					t.Fatalf("failed renewal changed the new owner: owner=%q err=%v", owner, err)
				}
			}
		})
	}
}

func TestRefreshOrderExpirationRuntimeFailureDoesNotExtendOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	order := seedRenewalOrder(t)
	injected := errors.New("injected runtime write failure")
	const callback = "test:fail-reservation-renewal"
	if err := dao.RuntimeDB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		tx.AddError(injected)
	}); err != nil {
		t.Fatal(err)
	}
	defer dao.RuntimeDB.Callback().Update().Remove(callback)
	if err := RefreshOrderExpiration(order.TradeId); !errors.Is(err, injected) {
		t.Fatalf("refresh error = %v, want injected runtime error", err)
	}
	after, err := GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	if !after.CreatedAt.StdTime().Equal(order.CreatedAt.StdTime()) {
		t.Fatal("order was extended without renewing its reservation")
	}
}

func TestRefreshOrderExpirationRollbackKeepsConservativeReservation(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	order := seedRenewalOrder(t)
	tx := dao.Mdb.Begin()
	defer tx.Rollback()
	if err := RefreshOrderExpirationWithTransaction(tx, order.TradeId); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	after, err := GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	if !after.CreatedAt.StdTime().Equal(order.CreatedAt.StdTime()) {
		t.Fatal("main transaction rollback did not preserve the old deadline")
	}
	var lock mdb.TransactionLock
	if err := dao.RuntimeDB.Where("trade_id = ?", order.TradeId).Take(&lock).Error; err != nil {
		t.Fatal(err)
	}
	if lock.ExpiresAt.Before(time.Now().Add(9 * time.Minute)) {
		t.Fatal("rollback exposed the target instead of retaining the longer reservation")
	}
}

func TestProviderSwitchRenewsExistingOnChainReservation(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	order := seedRenewalOrder(t)
	if err := dao.Mdb.Transaction(func(tx *gorm.DB) error {
		updated, err := MarkProviderSwitchParentSelectedWithTransaction(tx, order.TradeId)
		if err != nil {
			return err
		}
		if !updated {
			return constant.OrderStatusConflict
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refreshed, err := GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	var lock mdb.TransactionLock
	if err := dao.RuntimeDB.Where("trade_id = ?", order.TradeId).Take(&lock).Error; err != nil {
		t.Fatal(err)
	}
	if !refreshed.IsSelected || lock.ExpiresAt.Before(refreshed.CreatedAt.StdTime().Add(10*time.Minute)) {
		t.Fatal("provider switch did not preserve the parent's reservation through its new deadline")
	}
}
