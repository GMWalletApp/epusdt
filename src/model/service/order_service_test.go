package service

import (
	"fmt"
	"sync"
	"testing"

	"github.com/assimon/luuu/internal/testutil"
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/request"
	"github.com/assimon/luuu/util/constant"
)

const (
	testServiceTronAddr1 = "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7"
	testServiceTronAddr2 = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
)

func newCreateTransactionRequest(orderID string, amount float64) *request.CreateTransactionRequest {
	return &request.CreateTransactionRequest{
		OrderId:   orderID,
		Currency:  "CNY",
		Token:     "USDT",
		Network:   "tron",
		Amount:    amount,
		NotifyUrl: "https://merchant.example/callback",
	}
}

func TestCreateTransactionAssignsIncrementedAmountsAndLocks(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp1, err := CreateTransaction(newCreateTransactionRequest("order_1", 1))
	if err != nil {
		t.Fatalf("create first transaction: %v", err)
	}
	resp2, err := CreateTransaction(newCreateTransactionRequest("order_2", 1))
	if err != nil {
		t.Fatalf("create second transaction: %v", err)
	}

	if got := fmt.Sprintf("%.2f", resp1.ActualAmount); got != "1.00" {
		t.Fatalf("first actual amount = %s, want 1.00", got)
	}
	if got := fmt.Sprintf("%.2f", resp2.ActualAmount); got != "1.01" {
		t.Fatalf("second actual amount = %s, want 1.01", got)
	}
	if resp1.ReceiveAddress != testServiceTronAddr1 || resp2.ReceiveAddress != testServiceTronAddr1 {
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

func TestOrderProcessingMarksPaidAndReleasesLock(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1))
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

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1))
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

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp, err := CreateTransaction(newCreateTransactionRequest("order_1", 1))
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

	if _, err := data.AddWalletAddress(testServiceTronAddr1); err != nil {
		t.Fatalf("add wallet: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkTron, testServiceTronAddr2); err != nil {
		t.Fatalf("add wallet: %v", err)
	}

	resp1, err := CreateTransaction(newCreateTransactionRequest("order_1", 1))
	if err != nil {
		t.Fatalf("create first transaction: %v", err)
	}
	resp2, err := CreateTransaction(newCreateTransactionRequest("order_2", 2))
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
