package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/dromara/carbon/v2"
	"github.com/tidwall/gjson"
)

func TestFailedIndependentEVMReceiptMovesOrderToReview(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	ctx := context.Background()
	const network = mdb.NetworkEthereum
	const wallet = "0x1111111111111111111111111111111111111111"
	const contract = "0x2222222222222222222222222222222222222222"
	txID := "0x" + strings.Repeat("11", 32)
	scannedBlock := "0x" + strings.Repeat("cd", 32)
	independentBlock := "0x" + strings.Repeat("ef", 32)
	chain := mdb.Chain{Network: network, DisplayName: network, Enabled: true, MinConfirmations: 1}
	if err := dao.Mdb.Where("network = ?", network).Assign(map[string]any{"enabled": true, "min_confirmations": 1}).FirstOrCreate(&chain).Error; err != nil {
		t.Fatal(err)
	}
	token := mdb.ChainToken{Network: network, Symbol: "USDT", ContractAddress: contract, Decimals: 6, Enabled: true}
	if err := dao.Mdb.Where("network = ? AND symbol = ?", network, token.Symbol).Assign(token).FirstOrCreate(&token).Error; err != nil {
		t.Fatal(err)
	}
	order := mdb.Orders{TradeId: "failed-receipt-order", OrderId: "failed-receipt-order", Network: network, Token: token.Symbol, Amount: 1, ActualAmount: 1, ReceiveAddress: wallet, Status: mdb.StatusWaitPay}
	if err := dao.Mdb.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := data.LockTransaction(network, wallet, token.Symbol, order.TradeId, order.ActualAmount, time.Hour); err != nil {
		t.Fatal(err)
	}
	logData := fmt.Sprintf(`{"address":%q,"topics":["0x%s","0x%s","0x%s"],"data":"0x%064x","transactionHash":%q,"blockHash":%q}`, contract, transferSignature, strings.Repeat("0", 64), strings.Repeat("0", 24)+wallet[2:], 1_000_000, txID, scannedBlock)
	activity := scanner.Activity{
		Network: network, Height: 17, TxID: txID, TimeMS: time.Now().Add(time.Second).UnixMilli(),
		Confirmations: 1, Addresses: []string{wallet},
		Data: json.RawMessage(fmt.Sprintf(`{"transaction":{"hash":%q,"blockHash":%q,"blockNumber":"0x11","from":"0x3333333333333333333333333333333333333333","to":%q,"input":"0x","value":"0x0"},"logs":[%s],"receipt":{"transactionHash":%q,"blockHash":%q,"blockNumber":"0x11","status":"0x1","logs":[%s]}}`, txID, scannedBlock, contract, logData, txID, scannedBlock, logData)),
	}
	if err := ApplyChainActivity(ctx, activity); err != nil {
		t.Fatal(err)
	}
	independent := json.RawMessage(fmt.Sprintf(`{"transaction":{"hash":%q,"blockHash":%q,"blockNumber":"0x12","from":"0x3333333333333333333333333333333333333333","to":%q,"input":"0x","value":"0x0"},"receipt":{"transactionHash":%q,"blockHash":%q,"blockNumber":"0x12","status":"0x0","logs":[]}}`, txID, independentBlock, contract, txID, independentBlock))
	worked, err := processDueConfirmation(ctx, nil, func(context.Context, string, string, int64) (json.RawMessage, error) {
		return independent, nil
	})
	if err != nil || !worked {
		t.Fatalf("process confirmation: worked=%v err=%v", worked, err)
	}
	got, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil || got.Status != mdb.StatusManualReview {
		t.Fatalf("order status=%d err=%v, want manual review", got.Status, err)
	}
	callbacks, err := data.GetPendingCallbackOrders(3, 10)
	if err != nil || len(callbacks) != 0 {
		t.Fatalf("callback rows=%d err=%v", len(callbacks), err)
	}
}

func TestIndependentTronEvidenceMismatchMovesOrderToReview(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	raw, err := os.ReadFile("../../../chainscanner/internal/chains/testdata/strict/tron-native.json")
	if err != nil {
		t.Fatal(err)
	}
	txID := gjson.GetBytes(raw, "transaction.txID").String()
	height := gjson.GetBytes(raw, "receipt.blockNumber").Int()
	timestamp := gjson.GetBytes(raw, "receipt.blockTimeStamp").Int()
	recipient, err := scanner.Canonical(mdb.NetworkTron, gjson.GetBytes(raw, "transaction.raw_data.contract.0.parameter.value.to_address").String())
	if err != nil {
		t.Fatal(err)
	}
	chain := mdb.Chain{Network: mdb.NetworkTron, DisplayName: mdb.NetworkTron, Enabled: true, MinConfirmations: 1}
	if err = dao.Mdb.Where("network = ?", chain.Network).Assign(map[string]any{"enabled": true, "min_confirmations": 1}).FirstOrCreate(&chain).Error; err != nil {
		t.Fatal(err)
	}
	token := mdb.ChainToken{Network: mdb.NetworkTron, Symbol: "TRX", Decimals: 6, Enabled: true}
	if err = dao.Mdb.Where("network = ? AND symbol = ?", token.Network, token.Symbol).Assign(token).FirstOrCreate(&token).Error; err != nil {
		t.Fatal(err)
	}
	order := mdb.Orders{
		BaseModel:      mdb.BaseModel{CreatedAt: *carbon.NewTime(carbon.CreateFromTimestampMilli(timestamp - 1000))},
		TradeId:        "independent-tron-mismatch",
		OrderId:        "independent-tron-mismatch",
		Network:        mdb.NetworkTron,
		Token:          token.Symbol,
		Amount:         1,
		ActualAmount:   1,
		ReceiveAddress: recipient,
		Status:         mdb.StatusWaitPay,
	}
	if err = dao.Mdb.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err = data.LockTransaction(order.Network, order.ReceiveAddress, order.Token, order.TradeId, order.ActualAmount, time.Hour); err != nil {
		t.Fatal(err)
	}
	transaction := strings.Replace(gjson.GetBytes(raw, "transaction").Raw, `"amount": 800000`, `"amount": 1000000`, 1)
	if transaction == gjson.GetBytes(raw, "transaction").Raw {
		t.Fatal("failed to prepare inconsistent scan evidence")
	}
	activityData, err := json.Marshal(scanner.Evidence{
		Transaction: json.RawMessage(transaction),
		Receipt:     json.RawMessage(gjson.GetBytes(raw, "receipt").Raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	activity := scanner.Activity{
		Network: mdb.NetworkTron, Height: height, TxID: txID, TimeMS: timestamp,
		Confirmations: 10000, Addresses: []string{recipient}, Data: activityData,
	}
	if err = ApplyChainActivity(context.Background(), activity); err != nil {
		t.Fatal(err)
	}
	var job mdb.PaymentConfirmation
	if err = dao.Mdb.Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if job.SkipStrict {
		t.Fatal("strict payment verification unexpectedly disabled")
	}
	worked, err := processDueConfirmation(context.Background(), nil, func(context.Context, string, string, int64) (json.RawMessage, error) {
		return raw, nil
	})
	if err != nil || !worked {
		t.Fatalf("process confirmation: worked=%v err=%v", worked, err)
	}
	got, err := data.GetOrderInfoByTradeId(order.TradeId)
	if err != nil || got.Status != mdb.StatusManualReview {
		t.Fatalf("order status=%d err=%v, want manual review", got.Status, err)
	}
	callbacks, err := data.GetPendingCallbackOrders(3, 10)
	if err != nil || len(callbacks) != 0 {
		t.Fatalf("callback rows=%d err=%v", len(callbacks), err)
	}
}
