package mq

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/model/response"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/constant"
)

func TestExpiredOrderCleanupPreservesNewReservation(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}

	if _, err := data.AddWalletAddress("audit-wallet"); err != nil {
		t.Fatal(err)
	}
	create := func(id string) *response.CreateTransactionResponse {
		t.Helper()
		order, err := service.CreateTransaction(&request.CreateTransactionRequest{
			OrderId: id, Currency: "CNY", Token: "USDT", Network: mdb.NetworkTron,
			Amount: 1, NotifyUrl: "https://93.184.216.34/callback",
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return order
	}

	old := create("audit-old")
	// The lock expires before the asynchronous order-expiration worker runs.
	if err := dao.Mdb.Model(&mdb.Orders{}).Where("trade_id = ?", old.TradeId).
		UpdateColumn("created_at", time.Now().Add(-11*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).Where("trade_id = ?", old.TradeId).
		UpdateColumn("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}

	second := create("audit-second")
	if second.ActualAmount != old.ActualAmount {
		t.Fatal("expired amount was not reused")
	}
	processExpiredOrders()
	owner, err := data.GetTradeIdByWalletAddressAndAmountAndToken(
		mdb.NetworkTron, second.ReceiveAddress, second.Token, second.ActualAmount)
	if err != nil {
		t.Fatal(err)
	}
	if owner != second.TradeId {
		t.Fatalf("new reservation owner = %q, want %q", owner, second.TradeId)
	}

	third := create("audit-third")
	if second.ReceiveAddress != third.ReceiveAddress || second.ActualAmount == third.ActualAmount {
		t.Fatal("the next order must receive a different amount on the same wallet")
	}
	for _, id := range []string{second.TradeId, third.TradeId} {
		order, err := data.GetOrderInfoByTradeId(id)
		if err != nil || order.Status != mdb.StatusWaitPay {
			t.Fatalf("order %s is not pending: order=%+v err=%v", id, order, err)
		}
	}
	const txID = "audit-one-chain-payment"
	service.TryProcessTronTRC20Transfer(mdb.ChainToken{Symbol: "USDT", Decimals: 6},
		second.ReceiveAddress, big.NewInt(1_000_000), txID, time.Now().UnixMilli())
	secondPaid, err := data.GetOrderInfoByTradeId(second.TradeId)
	if err != nil || secondPaid.Status != mdb.StatusPaySuccess {
		t.Fatalf("payment was not credited to the reservation owner: order=%+v err=%v", secondPaid, err)
	}
	thirdPending, err := data.GetOrderInfoByTradeId(third.TradeId)
	if err != nil || thirdPending.Status != mdb.StatusWaitPay {
		t.Fatalf("unrelated order did not remain pending: order=%+v err=%v", thirdPending, err)
	}
	// Even when explicitly asked to settle the other order, the single-process
	// guard rejects reuse of the transaction that the scanner just credited.
	err = service.OrderProcessing(&request.OrderProcessingRequest{
		TradeId: third.TradeId, Network: mdb.NetworkTron, Token: "USDT",
		ReceiveAddress: third.ReceiveAddress, Amount: third.ActualAmount,
		BlockTransactionId: txID,
	})
	if !errors.Is(err, constant.OrderBlockAlreadyProcess) {
		t.Fatalf("duplicate settlement error = %v, want %v", err, constant.OrderBlockAlreadyProcess)
	}
	var paid int64
	if err := dao.Mdb.Model(&mdb.Orders{}).Where("status = ?", mdb.StatusPaySuccess).Count(&paid).Error; err != nil {
		t.Fatal(err)
	}
	if paid != 1 {
		t.Fatalf("one transaction settled %d orders, want exactly one", paid)
	}
}
