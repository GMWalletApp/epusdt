package task

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestScannerHTTPSubscriptionThroughOrderSettlement(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	ctx := context.Background()
	const net = "ethereum"
	const wallet = "0x1111111111111111111111111111111111111111"
	const contract = "0x2222222222222222222222222222222222222222"
	if err := dao.Mdb.Model(&mdb.Chain{}).Where("network = ?", net).Update("min_confirmations", 2).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{&mdb.WalletAddress{Network: net, Address: wallet}, &mdb.ChainToken{Network: net, Symbol: "FOO", ContractAddress: contract, Decimals: 6, Enabled: true}} {
		if err := dao.Mdb.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	order := mdb.Orders{TradeId: "scanner-payment", OrderId: "merchant-order", Network: net, Token: "FOO", Amount: 1, ActualAmount: 1, ReceiveAddress: wallet, Status: mdb.StatusWaitPay}
	if err := dao.Mdb.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	store, err := scanner.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token := strings.Repeat("p", 32)
	handler, err := scanner.Handler(store, map[string]string{paymentConsumer: token})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := scanner.Client{URL: server.URL, Token: token}
	sub := paymentSubscriptions{feed: client}
	if err = sub.ensure(net, wallet); err != nil {
		t.Fatal(err)
	}
	// Two transactions touch this wallet. Only the second contains an incoming transfer.
	amounts := []string{strings.Repeat("0", 63) + "1", fmt.Sprintf("%064x", 1_000_000)}
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage
			Method string
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch req.Method {
		case "eth_blockNumber":
			result = "0x12"
		case "eth_getBlockByNumber":
			result = map[string]any{"number": "0x11", "hash": "block", "timestamp": fmt.Sprintf("0x%x", time.Now().Unix()+1), "transactions": []any{map[string]any{"hash": "out", "from": wallet, "to": contract}, map[string]any{"hash": "in", "from": contract, "to": wallet}}}
		case "eth_getLogs":
			var logs []any
			for i, id := range []string{"out", "in"} {
				recipient := contract
				if i == 1 {
					recipient = wallet
				}
				logs = append(logs, map[string]any{"blockHash": "block", "transactionHash": id, "address": contract, "topics": []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", "0x" + strings.Repeat("0", 64), "0x" + strings.Repeat("0", 24) + recipient[2:]}, "data": "0x" + amounts[i]})
			}
			result = logs
		default:
			t.Error(req.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer rpc.Close()
	n := scanner.Network{Name: net, URL: rpc.URL, Confirmations: 2}
	src, err := scanner.NewSource(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err = store.Step(ctx, n, src); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		status int
	}{{"out", mdb.StatusWaitPay}, {"in", mdb.StatusPaySuccess}} {
		d, err := client.Pending(ctx, paymentConsumer)
		if err != nil || d == nil || d.Activity.TxID != tc.id {
			t.Fatal(tc, d, err)
		}
		for i := 0; i < 2; i++ {
			if err = processConfirmedActivity(ctx, client, d.Activity); err != nil {
				t.Fatal(err)
			}
		}
		got, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil || got.Status != tc.status {
			t.Fatal(tc, got, err)
		}
		if err = client.Ack(ctx, paymentConsumer, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if d, err := client.Pending(ctx, paymentConsumer); err != nil || d != nil {
		t.Fatal(d, err)
	}
}
