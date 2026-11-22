package data

import (
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestMerchantReportingAfterNetworkSwitch(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	created := time.Date(2026, 4, 23, 12, 15, 0, 0, time.UTC)
	insert := func(trade, parent, network string, status int, amount float64) mdb.Orders {
		t.Helper()
		order := mdb.Orders{TradeId: trade, OrderId: "merchant-" + trade, ParentTradeId: parent, Network: network,
			Token: "USDT", Status: status, Amount: 100, Currency: "CNY", ActualAmount: amount, ReceiveAddress: network + "-address"}
		if err := dao.Mdb.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
		if err := dao.Mdb.Model(&order).UpdateColumns(map[string]any{"created_at": created, "updated_at": created.Add(time.Minute)}).Error; err != nil {
			t.Fatal(err)
		}
		return order
	}
	parent := insert("first", "", "tron", mdb.StatusPaySuccess, 99)
	child := insert("winner", parent.TradeId, "bsc", mdb.StatusPaySuccess, 11.5)
	insert("abandoned", parent.TradeId, "base", mdb.StatusExpired, 42)
	if err := dao.Mdb.Model(&child).UpdateColumns(map[string]any{"block_transaction_id": "tx-winner", "token": "USDC", "pay_provider": mdb.PaymentProviderXLayer}).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Model(&parent).UpdateColumns(map[string]any{"pay_by_sub_id": child.ID, "callback_confirm": mdb.CallBackConfirmOk, "callback_num": 3}).Error; err != nil {
		t.Fatal(err)
	}
	direct := insert("direct", "", "tron", mdb.StatusPaySuccess, 7)
	if err := dao.Mdb.Model(&direct).UpdateColumn("parent_trade_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	insert("unpaid", "", "tron", mdb.StatusExpired, 9)
	start, end := created.Add(-time.Minute), created.Add(time.Minute)
	f := OrderListFilter{Statuses: []int{mdb.StatusPaySuccess}, Networks: []string{"bsc"}, Token: "usdc", Address: "bsc-address", Keyword: "tx-winner", PageSize: 1}
	rows, n, err := ListOrders(f)
	if err != nil || n != 1 || len(rows) != 1 {
		t.Fatalf("filtered list %v, %d: %v", rows, n, err)
	}
	got := rows[0]
	if got.TradeId != parent.TradeId || got.OrderId != parent.OrderId || got.ActualAmount != 11.5 || got.ReceiveAddress != child.ReceiveAddress ||
		got.Token != "USDC" || got.Network != "bsc" || got.BlockTransactionId != "tx-winner" || got.PayBySubId != child.ID ||
		got.CallBackConfirm != mdb.CallBackConfirmOk || got.CallbackNum != 3 || got.PayProvider != mdb.PaymentProviderXLayer {
		t.Errorf("merchant row: %+v", got)
	}
	if rows, err := OrdersForExport(f); err != nil || len(rows) != 1 || rows[0].TradeId != got.TradeId {
		t.Errorf("export %v: %v", rows, err)
	}
	raw, err := GetOrderInfoByTradeId(parent.TradeId)
	if err != nil || raw.Network != "tron" || raw.BlockTransactionId != "" {
		t.Errorf("stored parent %v: %v", raw, err)
	}
	f = OrderListFilter{Statuses: []int{mdb.StatusPaySuccess}, Networks: []string{"tron", "bsc"}, PageSize: 1, Page: 2}
	if rows, n, err := ListOrders(f); err != nil || n != 2 || len(rows) != 1 || rows[0].TradeId != parent.TradeId {
		t.Errorf("paged list %v, count %d: %v", rows, n, err)
	}
	if rows, n, err := ListOrders(OrderListFilter{Keyword: "abandoned"}); err != nil || n != 1 || len(rows) != 1 || rows[0].TradeId != parent.TradeId {
		t.Errorf("find abandoned attempt: %v, %d, %v", rows, n, err)
	}
	if rows, err := RecentOrders(10); err != nil || len(rows) != 3 || rows[2].Network != "bsc" {
		t.Errorf("recent orders %v: %v", rows, err)
	}
	total, success, sum, err := PaidStatsInRange(start, end)
	if err != nil || total != 3 || success != 2 || sum != 18.5 {
		t.Errorf("overview %d/%d, amount %v: %v", success, total, sum, err)
	}
	if sum, err := SumPaidActualAmount(); err != nil || sum != 18.5 {
		t.Errorf("all-time amount %v: %v", sum, err)
	}
	if n, err := CountExpiredInRange(start, end); err != nil || n != 1 {
		t.Errorf("expired %d: %v", n, err)
	}
	if n, err := ActiveAddressCountInRange(start, end); err != nil || n != 2 {
		t.Errorf("active addresses %d: %v", n, err)
	}
	if secs, err := AveragePaymentDurationSeconds(start, end); err != nil || secs != 60 {
		t.Errorf("duration %f: %v", secs, err)
	}
	for _, hourly := range []bool{true, false} {
		rows, err := queryOrderStats(start, end, hourly)
		if err != nil || len(rows) != 1 || rows[0].OrderCount != 3 || rows[0].SuccessCount != 2 || rows[0].ActualAmount != 18.5 || rows[0].TotalAmount != 200 {
			t.Errorf("order trend %v: %v", rows, err)
		}
		assets, err := queryAssetByAddress(start, end, hourly)
		if err != nil || len(assets) != 2 || assets[0].Address != "bsc-address" || assets[0].ActualAmount != 11.5 || assets[1].Address != "tron-address" || assets[1].ActualAmount != 7 {
			t.Errorf("asset trend %v: %v", assets, err)
		}
	}
	// Historical parents copied the hash but retained the original network.
	if err := dao.Mdb.Model(&parent).UpdateColumns(map[string]any{"pay_by_sub_id": 0, "block_transaction_id": "tx-winner"}).Error; err != nil {
		t.Fatal(err)
	}
	projected, err := GetAdminOrder(parent.TradeId)
	if err != nil || projected.Network != "bsc" || projected.PayBySubId != child.ID {
		t.Fatalf("legacy parent: %+v, %v", projected, err)
	}
	// Wrong-family links cannot disclose a different merchant's payment.
	if err := dao.Mdb.Model(&direct).UpdateColumn("pay_by_sub_id", child.ID).Error; err != nil {
		t.Fatal(err)
	}
	projected, err = GetAdminOrder(direct.TradeId)
	if err != nil || projected.Network != "" || projected.PayBySubId != 0 || projected.ActualAmount != 0 {
		t.Fatalf("unrelated link: %+v, %v", projected, err)
	}
}
