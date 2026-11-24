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
)

func TestAdditionalNetworkSettlement(t *testing.T) {
	for _, network := range []string{"arbitrum", "hyperevm", "monad", "arc", "avalanche", "optimism", "base", "sui"} {
		t.Run(network, func(t *testing.T) {
			defer testutil.SetupTestDatabases(t)()
			chain := mdb.Chain{Network: network, DisplayName: network, Enabled: true, MinConfirmations: 1}
			if err := dao.Mdb.Where("network = ?", network).Assign(map[string]any{"enabled": true}).FirstOrCreate(&chain).Error; err != nil {
				t.Fatal(err)
			}

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
			e := scanner.Activity{Network: network, TxID: "digest", Height: 17, Confirmations: 1000, Addresses: []string{address}, TimeMS: time.Now().Add(time.Second).UnixMilli()}
			for _, success := range []bool{false, true, true} {
				if network == "sui" {
					e.Data = json.RawMessage(fmt.Sprintf(`{"digest":"digest","transaction":{"sender":"0x1"},"effects":{"status":{"success":%t}},"balance_changes":[{"address":"0x1","coin_type":"0x2::sui::SUI","amount":"-1000001010"},{"address":%q,"coin_type":"0x2::sui::SUI","amount":"1000000000"}]}`, success, address))
				} else {
					status := "0x0"
					if success {
						status = "0x1"
					}
					var err error
					e.Data, err = json.Marshal(scanner.Evidence{
						Transaction: json.RawMessage(fmt.Sprintf(`{"hash":"digest","from":"0x1111111111111111111111111111111111111111","to":%q,"value":"0xde0b6b3a7640000","blockHash":"block","blockNumber":"0x11"}`, address)),
						Receipt:     json.RawMessage(fmt.Sprintf(`{"transactionHash":"digest","blockHash":"block","blockNumber":"0x11","status":%q,"logs":[]}`, status)),
					})
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
					want = mdb.StatusConfirming
				}
				if got.Status != want {
					t.Fatal("payment status", got.Status, want)
				}
			}
			var job mdb.PaymentConfirmation
			if err := dao.Mdb.Take(&job).Error; err != nil {
				t.Fatal(err)
			}
			result := e.Data
			if network == "sui" {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(result, &fields); err != nil {
					t.Fatal(err)
				}
				fields["checkpoint"] = json.RawMessage("17")
				result, _ = json.Marshal(fields)
			} else {
				result = json.RawMessage(fmt.Sprintf(`{"transaction":{"hash":"digest","from":"0x1111111111111111111111111111111111111111","to":%q,"value":"0xde0b6b3a7640000","blockHash":"block"},"receipt":{"transactionHash":"digest","blockHash":"block","blockNumber":"0x11","status":"0x1","logs":[]}}`, address))
			}
			if err := ConfirmPayment(t.Context(), nil, func(context.Context, string, string) (json.RawMessage, error) { return result, nil }, job); err != nil {
				t.Fatal(err)
			}
			got, err := data.GetOrderInfoByTradeId(order.TradeId)
			if err != nil || got.Status != mdb.StatusPaySuccess {
				t.Fatal(got, err)
			}

		})
	}
}

func TestBaseUSDCSettlementUsesSeededContract(t *testing.T) {
	testSeededERC20Settlement(t, mdb.NetworkBase, "USDC",
		"0x833589fcd6edb6e08f4c7c32d4f71b54bda02913", "0xd9aaec86b65d86f6a7b5b1b0c42ffa531710b6ca")
}

func TestArbitrumUSDTSettlementUsesSeededContract(t *testing.T) {
	testSeededERC20Settlement(t, mdb.NetworkArbitrum, "USDT",
		"0xfd086bc7cd5c481dcc9c85ebe478a1c0b69fcbb9", "0xaf88d065e77c8cc2239327c5edb3a432268e5831")
}

func testSeededERC20Settlement(t *testing.T, network, symbol, contract, otherContract string) {
	t.Helper()
	defer testutil.SetupTestDatabases(t)()
	dao.ResetMdbTableInitForTest()
	defer dao.ResetMdbTableInitForTest()
	dao.MdbTableInit()
	const recipient = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	order := mdb.Orders{TradeId: "seeded-token", OrderId: "order", Network: network,
		Token: symbol, Amount: 1, ActualAmount: 1, ReceiveAddress: recipient, Status: mdb.StatusWaitPay}
	if err := dao.Mdb.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := data.LockTransaction(order.Network, recipient, order.Token, order.TradeId, 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		contract string
		want     int
	}{
		{otherContract, mdb.StatusWaitPay},
		{contract, mdb.StatusConfirming},
	} {
		log := json.RawMessage(fmt.Sprintf(`{"address":%q,"topics":["0x%s","0x%064x","0x%064s"],"data":"0x%064x"}`, tc.contract, transferSignature, 1, recipient[2:], 1000000))
		evidence, err := json.Marshal(scanner.Evidence{Transaction: json.RawMessage(`{"hash":"digest"}`), Logs: []json.RawMessage{log}})
		if err != nil {
			t.Fatal(err)
		}
		event := scanner.Activity{Network: order.Network, TxID: "digest", Addresses: []string{recipient}, TimeMS: time.Now().Add(time.Second).UnixMilli(), Data: evidence}
		if err := ApplyChainActivity(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		got, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tc.want {
			t.Fatalf("contract %s: status %d, want %d", tc.contract, got.Status, tc.want)
		}
	}
}
