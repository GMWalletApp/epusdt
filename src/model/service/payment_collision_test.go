package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"gorm.io/gorm"
)

func TestConcurrentOrdersGetDistinctPaymentTargets(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}

	if _, err := data.AddWalletAddress("audit-wallet"); err != nil {
		t.Fatal(err)
	}

	const count = 40
	results := make(chan string, count)
	errs := make(chan error, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			resp, err := CreateTransaction(newCreateTransactionRequest(fmt.Sprintf("audit-%d", i), 1), nil)
			if err != nil {
				errs <- err
				return
			}
			results <- fmt.Sprintf("%s:%s:%.6f", resp.ReceiveAddress, resp.Token, resp.ActualAmount)
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for target := range results {
		if seen[target] {
			t.Fatalf("duplicate payment target: %s", target)
		}
		seen[target] = true
	}
	if len(seen) != count {
		t.Fatalf("unique targets = %d, want %d", len(seen), count)
	}
	t.Logf("%d concurrent orders on one wallet received %d distinct amounts", count, len(seen))
}

func TestSwitchNetworkExtendsParentAndItsReservation(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}

	if _, err := data.AddWalletAddress("audit-parent-wallet"); err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0x1111111111111111111111111111111111111111"); err != nil {
		t.Fatal(err)
	}
	parent, err := CreateTransaction(newCreateTransactionRequest("audit-parent", 1), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Model an order created nine minutes ago with one minute left on its lock.
	now := time.Now()
	oldExpiry := now.Add(time.Minute)
	if err := dao.Mdb.Model(&mdb.Orders{}).Where("trade_id = ?", parent.TradeId).
		UpdateColumn("created_at", now.Add(-9*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).Where("trade_id = ?", parent.TradeId).
		UpdateColumn("expires_at", oldExpiry).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := SwitchNetwork(&request.SwitchNetworkRequest{
		TradeId: parent.TradeId,
		Token:   "USDT",
		Network: mdb.NetworkEthereum,
	}); err != nil {
		t.Fatal(err)
	}

	var lock mdb.TransactionLock
	if err := dao.RuntimeDB.Where("trade_id = ?", parent.TradeId).Take(&lock).Error; err != nil {
		t.Fatal(err)
	}
	if !lock.ExpiresAt.After(oldExpiry) {
		t.Fatal("parent reservation was not refreshed")
	}
	refreshed, err := data.GetOrderInfoByTradeId(parent.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.CreatedAt.Timestamp() < now.Unix() {
		t.Fatal("parent expiration was not refreshed")
	}
	if lock.ExpiresAt.Before(refreshed.CreatedAt.StdTime().Add(10 * time.Minute)) {
		t.Fatal("reservation expires before the refreshed order")
	}

	// Cleanup at the original deadline must leave the renewed reservation intact.
	if err := dao.RuntimeDB.Where("expires_at <= ?", oldExpiry).Delete(&mdb.TransactionLock{}).Error; err != nil {
		t.Fatal(err)
	}
	next, err := CreateTransaction(newCreateTransactionRequest("audit-next", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.ReceiveAddress != parent.ReceiveAddress || next.ActualAmount == parent.ActualAmount {
		t.Fatal("the next order must receive a different amount on the same wallet")
	}
	if refreshed.Status != mdb.StatusWaitPay || next.Status != mdb.StatusWaitPay {
		t.Fatal("both independent parent orders must still be waiting")
	}
}

func TestSwitchNetworkReusingChildRenewsParentReservation(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddress("reuse-parent-wallet"); err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0x1111111111111111111111111111111111111111"); err != nil {
		t.Fatal(err)
	}
	parent, err := CreateTransaction(newCreateTransactionRequest("reuse-parent", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := &request.SwitchNetworkRequest{TradeId: parent.TradeId, Token: "USDT", Network: mdb.NetworkEthereum}
	child, err := SwitchNetwork(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Model(&mdb.Orders{}).Where("trade_id = ?", parent.TradeId).
		UpdateColumn("created_at", time.Now().Add(-9*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).Where("trade_id = ?", parent.TradeId).
		UpdateColumn("expires_at", time.Now().Add(time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	reused, err := SwitchNetwork(req)
	if err != nil {
		t.Fatal(err)
	}
	if reused.TradeId != child.TradeId {
		t.Fatal("switch created another child instead of reusing it")
	}
	var lock mdb.TransactionLock
	if err := dao.RuntimeDB.Where("trade_id = ?", parent.TradeId).Take(&lock).Error; err != nil {
		t.Fatal(err)
	}
	refreshed, err := data.GetOrderInfoByTradeId(parent.TradeId)
	if err != nil {
		t.Fatal(err)
	}
	if lock.ExpiresAt.Before(refreshed.CreatedAt.StdTime().Add(10 * time.Minute)) {
		t.Fatal("reusing a child extended the parent without its reservation")
	}
}

func TestSwitchNetworkFailedRenewalRollsBackNewChild(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddress("conflict-parent-wallet"); err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkEthereum, "0x1111111111111111111111111111111111111111"); err != nil {
		t.Fatal(err)
	}
	parent, err := CreateTransaction(newCreateTransactionRequest("conflict-parent", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.UnLockTransactionByTradeId(parent.TradeId); err != nil {
		t.Fatal(err)
	}
	req := &request.SwitchNetworkRequest{TradeId: parent.TradeId, Token: "USDT", Network: mdb.NetworkEthereum}
	if _, err := SwitchNetwork(req); !errors.Is(err, constant.OrderStatusConflict) {
		t.Fatalf("switch error = %v, want status conflict", err)
	}
	count, err := data.CountSubOrders(parent.TradeId)
	if err != nil || count != 0 {
		t.Fatalf("failed switch left %d child orders: %v", count, err)
	}
	var locks int64
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).Count(&locks).Error; err != nil || locks != 0 {
		t.Fatalf("failed switch leaked %d reservations: %v", locks, err)
	}
}

func TestLateSettlementDoesNotReleaseReassignedTarget(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddress("late-payment-wallet"); err != nil {
		t.Fatal(err)
	}
	old, err := CreateTransaction(newCreateTransactionRequest("late-payment-old", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Model(&mdb.Orders{}).Where("trade_id = ?", old.TradeId).
		UpdateColumn("status", mdb.StatusExpired).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).Where("trade_id = ?", old.TradeId).
		UpdateColumn("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	current, err := CreateTransaction(newCreateTransactionRequest("late-payment-new", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if current.ActualAmount != old.ActualAmount {
		t.Fatal("expired target was not reused")
	}
	if err := orderProcessingWithAllowedStatuses(&request.OrderProcessingRequest{
		TradeId: old.TradeId, BlockTransactionId: "previous-payment",
	}, adminManualPaymentStatuses); err != nil {
		t.Fatal(err)
	}
	owner, err := data.GetTradeIdByWalletAddressAndAmountAndToken(
		mdb.NetworkTron, current.ReceiveAddress, current.Token, current.ActualAmount)
	if err != nil || owner != current.TradeId {
		t.Fatalf("settling the old order changed the new owner: owner=%q err=%v", owner, err)
	}
}

func TestParentSettlementRetainsLockWhenChildExpirationFails(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddress("expire-failure-wallet"); err != nil {
		t.Fatal(err)
	}
	parent, err := CreateTransaction(newCreateTransactionRequest("expire-failure-parent", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	child := &mdb.Orders{
		TradeId: "expire-failure-child", OrderId: "expire-failure-child", ParentTradeId: parent.TradeId,
		Network: mdb.NetworkTron, Token: "USDT", ReceiveAddress: parent.ReceiveAddress,
		ActualAmount: 2, Status: mdb.StatusWaitPay,
	}
	if err := dao.Mdb.Create(child).Error; err != nil {
		t.Fatal(err)
	}
	if err := data.LockTransaction(child.Network, child.ReceiveAddress, child.Token, child.TradeId, child.ActualAmount, time.Hour); err != nil {
		t.Fatal(err)
	}
	const callback = "test:fail-child-expiration"
	if err := dao.Mdb.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if updates, ok := tx.Statement.Dest.(map[string]interface{}); ok && updates["status"] == mdb.StatusExpired {
			tx.AddError(errors.New("injected child expiration failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer dao.Mdb.Callback().Update().Remove(callback)
	if err := OrderProcessing(&request.OrderProcessingRequest{
		TradeId: parent.TradeId, BlockTransactionId: "expire-failure-payment",
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := data.GetOrderInfoByTradeId(child.TradeId)
	if err != nil || pending.Status != mdb.StatusWaitPay {
		t.Fatalf("child did not remain pending after the injected failure: order=%+v err=%v", pending, err)
	}
	owner, err := data.GetTradeIdByWalletAddressAndAmountAndToken(child.Network, child.ReceiveAddress, child.Token, child.ActualAmount)
	if err != nil || owner != child.TradeId {
		t.Fatalf("failed child expiration released its reservation: owner=%q err=%v", owner, err)
	}
}
