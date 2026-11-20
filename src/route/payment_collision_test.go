package route

import (
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestAdminCloseOrderPreservesReassignedReservation(t *testing.T) {
	e, token := setupAdminTestEnv(t)
	old := &mdb.Orders{
		TradeId: "admin-close-old", OrderId: "admin-close-old",
		Network: mdb.NetworkTron, Token: "USDT", ReceiveAddress: "TTestTronAddress001",
		ActualAmount: 1, Status: mdb.StatusWaitPay,
	}
	if err := dao.Mdb.Create(old).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Model(old).UpdateColumn("created_at", time.Now().Add(-11*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := data.LockTransaction(old.Network, old.ReceiveAddress, old.Token, old.TradeId, old.ActualAmount, -time.Minute); err != nil {
		t.Fatal(err)
	}
	current := &mdb.Orders{
		TradeId: "admin-close-new", OrderId: "admin-close-new",
		Network: old.Network, Token: old.Token, ReceiveAddress: old.ReceiveAddress,
		ActualAmount: old.ActualAmount, Status: mdb.StatusWaitPay,
	}
	if err := dao.Mdb.Create(current).Error; err != nil {
		t.Fatal(err)
	}
	if err := data.LockTransaction(current.Network, current.ReceiveAddress, current.Token, current.TradeId, current.ActualAmount, time.Hour); err != nil {
		t.Fatal(err)
	}

	rec := doPostAdmin(e, "/admin/api/v1/orders/"+old.TradeId+"/close", nil, token)
	assertOK(t, rec)
	closed, err := data.GetOrderInfoByTradeId(old.TradeId)
	if err != nil || closed.Status != mdb.StatusExpired {
		t.Fatalf("old order was not closed: order=%+v err=%v", closed, err)
	}
	owner, err := data.GetTradeIdByWalletAddressAndAmountAndToken(current.Network, current.ReceiveAddress, current.Token, current.ActualAmount)
	if err != nil || owner != current.TradeId {
		t.Fatalf("closing the old order changed the new owner: owner=%q err=%v", owner, err)
	}
}
