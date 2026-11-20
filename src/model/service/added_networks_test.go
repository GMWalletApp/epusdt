package service

import (
	"context"
	"encoding/json"
	"fmt"
	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"strings"
	"testing"
	"time"
)

func TestAdditionalNetworkSettlement(t *testing.T) {
	for _, network := range []string{"arbitrum", "hyperevm", "monad", "arc", "avalanche", "optimism", "sui"} {
		t.Run(network, func(t *testing.T) {
			defer testutil.SetupTestDatabases(t)()
			address := "0x" + strings.Repeat("a", 40)
			token := mdb.ChainToken{Network: network, Symbol: scanner.NativeAsset(network), Enabled: true, Decimals: 18}
			if network == "sui" {
				address, _ = scanner.Canonical("sui", "0xaa")
				token.Symbol = "SUI"
				token.Decimals = 9
				token.ContractAddress = "0x2::sui::SUI"
			}
			if network == "arc" {
				token.Decimals = 6
				token.ContractAddress = "0x3600000000000000000000000000000000000000"
			}
			if !ChainTokenReadyForPayment(token) {
				t.Fatal("asset unavailable")
			}
			if err := dao.Mdb.Create(&token).Error; err != nil {
				t.Fatal(err)
			}
			order := mdb.Orders{TradeId: "trade-" + network, OrderId: "order", Network: network, Token: token.Symbol, Amount: 1, ActualAmount: 1, ReceiveAddress: address, Status: mdb.StatusWaitPay}
			if err := dao.Mdb.Create(&order).Error; err != nil {
				t.Fatal(err)
			}
			if err := data.LockTransaction(network, address, order.Token, order.TradeId, 1, time.Minute); err != nil {
				t.Fatal(err)
			}
			e := scanner.Activity{Network: network, TxID: "digest", Addresses: []string{address}, TimeMS: time.Now().Add(time.Second).UnixMilli()}
			for _, success := range []bool{false, true, true} {
				if network == "sui" {
					e.Data = json.RawMessage(fmt.Sprintf(`{"digest":"digest","transaction":{"sender":"0x1"},"effects":{"status":{"success":%t}},"balance_changes":[{"address":"0x1","coin_type":"0x2::sui::SUI","amount":"-1000001010"},{"address":%q,"coin_type":"0x2::sui::SUI","amount":"1000000000"}]}`, success, address))
				} else {
					status := "0x0"
					if success {
						status = "0x1"
					}
					var err error
					e.Data, err = json.Marshal(scanner.Evidence{Transaction: json.RawMessage(fmt.Sprintf(`{"hash":"digest","from":"0x1111111111111111111111111111111111111111","to":%q,"value":"0xde0b6b3a7640000"}`, address)), Receipt: json.RawMessage(fmt.Sprintf(`{"transactionHash":"digest","status":%q}`, status))})
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := ApplyChainActivity(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				got, err := data.GetOrderInfoByTradeId(order.TradeId)
				if err != nil {
					t.Fatal(err)
				}
				want := mdb.StatusWaitPay
				if success {
					want = mdb.StatusPaySuccess
				}
				if got.Status != want {
					t.Fatal("payment status", got.Status, want)
				}
			}
		})
	}
}
