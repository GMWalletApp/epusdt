package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
)

func TestA7A5Payment(t *testing.T) {
	for network, contract := range map[string]string{
		"tron":     "751f6515e355dc49474a8565a1234bf3424b9fe4",
		"ethereum": "0x6fa0be17e4bea2fcfa22ef89bf8ac9aab0ab0fc9",
	} {
		t.Run(network, func(t *testing.T) {
			defer testutil.SetupTestDatabases(t)()
			dao.ResetMdbTableInitForTest()
			defer dao.ResetMdbTableInitForTest()
			dao.MdbTableInit()
			recipient, err := scanner.Canonical(network, strings.Repeat("11", 20))
			if err != nil {
				t.Fatal(err)
			}
			if err := dao.Mdb.Create(&mdb.WalletAddress{Network: network, Address: recipient, Status: mdb.TokenStatusEnable}).Error; err != nil {
				t.Fatal(err)
			}
			req := &request.CreateTransactionRequest{OrderId: "a7a5-order", Amount: 25, Currency: "RUB", Token: "A7A5", Network: network, NotifyUrl: "https://93.184.216.34/notify"}
			if _, err := CreateTransaction(req, nil); err != constant.SupportedAssetNotFound {
				t.Fatalf("asset should start disabled, got %v", err)
			}
			if err := dao.Mdb.Model(&mdb.ChainToken{}).Where("network = ? AND symbol = ?", network, "A7A5").Update("enabled", true).Error; err != nil {
				t.Fatal(err)
			}
			dao.ResetMdbTableInitForTest()
			dao.MdbTableInit()
			if _, err := CreateTransaction(req, nil); err != constant.RateAmountErr {
				t.Fatalf("enabled asset with missing quote: %v", err)
			}
			if err := data.SetSetting("rate", mdb.SettingKeyRateForcedRateList, `{"rub":{"a7a5":0.5}}`, "json"); err != nil {
				t.Fatal(err)
			}
			order, err := CreateTransaction(req, nil)
			if err != nil || order.ActualAmount != 12.5 {
				t.Fatalf("order=%+v err=%v", order, err)
			}
			event := scanner.Activity{Network: network, Addresses: []string{recipient}, TxID: "0x1234", Height: 10, Confirmations: 1000, TimeMS: time.Now().Add(time.Second).UnixMilli()}
			for _, emitter := range []string{strings.Repeat("aa", 20), contract} {
				log := fmt.Sprintf(`{"address":%q,"topics":[%q,"%064x","%024x%s"],"data":"%064x","transactionHash":"0x1234","blockHash":"0xab"}`, emitter, transferSignature, 2, 0, strings.Repeat("11", 20), 12500000)
				var evidence scanner.Evidence
				if network == "tron" {
					evidence.Transaction = json.RawMessage(`{"txID":"0x1234","ret":[{"contractRet":"SUCCESS"}]}`)
					evidence.Receipt = json.RawMessage(fmt.Sprintf(`{"id":"0x1234","blockNumber":10,"blockTimeStamp":%d,"log":[%s]}`, event.TimeMS, log))
				} else {
					evidence.Transaction = json.RawMessage(`{"hash":"0x1234","blockHash":"0xab"}`)
					evidence.Receipt = json.RawMessage(fmt.Sprintf(`{"transactionHash":"0x1234","blockHash":"0xab","blockNumber":"0xa","status":"0x1","logs":[%s]}`, log))
					evidence.Logs = []json.RawMessage{json.RawMessage(log)}
				}
				event.Data, err = json.Marshal(evidence)
				if err != nil {
					t.Fatal(err)
				}
				if err := ApplyChainActivity(t.Context(), event); err != nil {
					t.Fatal(err)
				}
				stored, err := data.GetOrderInfoByTradeId(order.TradeId)
				want := mdb.StatusWaitPay
				if emitter == contract {
					want = mdb.StatusConfirming
				}
				if err != nil || stored.Status != want {
					t.Fatalf("emitter=%s order=%+v err=%v", emitter, stored, err)
				}
			}
			var job mdb.PaymentConfirmation
			if err := dao.Mdb.Take(&job).Error; err != nil {
				t.Fatal(err)
			}
			if err := ConfirmPayment(t.Context(), nil, func(context.Context, string, string, int64) (json.RawMessage, error) { return event.Data, nil }, job); err != nil {
				t.Fatal(err)
			}
			paid, err := data.GetOrderInfoByTradeId(order.TradeId)
			if err != nil || paid.Status != mdb.StatusPaySuccess || paid.BlockTransactionId != event.TxID {
				t.Fatalf("payment=%+v err=%v", paid, err)
			}
		})
	}
}
