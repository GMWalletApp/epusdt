package task

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
)

func TestScannerHTTPSubscriptionThroughOrderSettlement(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	ctx := context.Background()
	const net = "ethereum"
	incoming := "0x" + strings.Repeat("1a", 32)
	outgoing := "0x" + strings.Repeat("2b", 32)
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
	// This fixture starts with a previous committed execution block.
	if err = store.SeedCursor(ctx, net, 16); err != nil {
		t.Fatal(err)
	}
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
		case "eth_chainId":
			result = "0x1"
		case "eth_blockNumber":
			result = "0x12"
		case "eth_getBlockByNumber":
			result = map[string]any{"number": "0x11", "hash": "block", "parentHash": "parent", "timestamp": fmt.Sprintf("0x%x", time.Now().Unix()+1), "transactions": []any{map[string]any{"hash": outgoing, "from": wallet, "to": contract}, map[string]any{"hash": incoming, "from": contract, "to": wallet}}}
		case "eth_getLogs":
			var logs []any
			for i, id := range []string{outgoing, incoming} {
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
	// Drive the payment worker through its normal database node selection.
	if err = dao.Mdb.Model(&mdb.RpcNode{}).Where("network = ?", net).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "busy", http.StatusTooManyRequests) }))
	defer unavailable.Close()
	for i, url := range []string{unavailable.URL, rpc.URL} {
		node := mdb.RpcNode{Network: net, Type: "http", Url: url, Enabled: true, Weight: 100 - i}
		if err = data.CreateRpcNode(&node); err != nil {
			t.Fatal(err)
		}
	}
	worker := embeddedWorker{name: net, store: store}
	defer worker.close()
	if _, err = worker.step(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		status int
	}{{outgoing, mdb.StatusWaitPay}, {incoming, mdb.StatusConfirming}} {
		d, err := client.PendingNetwork(ctx, paymentConsumer, net, 0)
		if err != nil || d == nil || d.Activity.TxID != tc.id {
			t.Fatal(tc, d, err)
		}
		for i := 0; i < 2; i++ {
			if err = processScannerActivity(ctx, d.Activity); err != nil {
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
	if d, err := client.PendingNetwork(ctx, paymentConsumer, net, 0); err != nil || d != nil {
		t.Fatal(d, err)
	}
	// After feed acknowledgement a fresh worker must find the order in durable storage.
	var jobs []mdb.PaymentConfirmation
	if err := dao.Mdb.Find(&jobs).Error; err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%d err=%v", len(jobs), err)
	}
	job := jobs[0]
	registry, err := scanner.NewNodeRegistry(ctx, store, []scanner.Network{{Name: net}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := registry.List(ctx, net)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range rows {
		if err := registry.Delete(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	h, err := registry.Handler(map[string]string{paymentConsumer: token}, "")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(h)
	defer api.Close()
	remote := newStrictClient(scanner.Client{URL: api.URL, Token: token}, nil)
	embedded := newStrictClient(scanner.Client{Token: token}, h)
	var mode atomic.Int32
	counts := [2]atomic.Int32{}
	for i := range 2 {
		node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			case "eth_chainId":
				result = "0x1"
			case "eth_blockNumber":
				result = "0x12"
			case "eth_getTransactionByHash":
				counts[i].Add(1)
				result = map[string]any{"hash": incoming, "from": contract, "to": wallet, "input": "0x", "blockHash": "block", "blockNumber": "0x11"}
			case "eth_getTransactionReceipt":
				amount := amounts[1]
				if mode.Load() == 0 || mode.Load() == 1 && i == 1 {
					amount = amounts[0]
				}
				result = map[string]any{"transactionHash": incoming, "blockHash": "block", "blockNumber": "0x11", "status": "0x1", "logs": []any{map[string]any{"address": contract, "transactionHash": incoming, "blockHash": "block", "data": "0x" + amount, "topics": []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", "0x" + strings.Repeat("0", 64), "0x" + strings.Repeat("0", 24) + wallet[2:]}}}}
			default:
				t.Errorf("unexpected RPC %s", req.Method)
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		}))
		defer node.Close()
		if _, err := registry.Add(ctx, scanner.NodeOptions{NodeEndpoint: scanner.NodeEndpoint{Network: net, Protocol: "http", URL: node.URL}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, phase := range []int32{0, 1, 2} {
		mode.Store(phase)
		lookup := remote
		if phase == 1 {
			lookup = embedded
		}
		if phase < 2 {
			if err := service.ConfirmPayment(ctx, client, lookup.full, job); err == nil {
				t.Fatalf("phase %d accepted unverified transfer", phase)
			}
			expired, err := data.UpdateOrderIsExpirationById(order.ID, time.Now().Add(time.Hour))
			if err != nil || expired {
				t.Fatal("expired confirming payment", err)
			}
		} else {
			// Start a new worker with no in-memory event after the queue was acknowledged.
			run, cancel := context.WithTimeout(ctx, 5*time.Second)
			stopped := make(chan struct{})
			go func() { defer close(stopped); service.RunPaymentConfirmations(run, client, lookup.round) }()
			var left int64
			for {
				if err := dao.Mdb.Model(&mdb.PaymentConfirmation{}).Count(&left).Error; err != nil {
					cancel()
					<-stopped
					t.Fatal(err)
				}
				if left == 0 || run.Err() != nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			<-stopped
			if left != 0 {
				t.Fatal("restarted worker did not finish the persisted payment")
			}
		}
		got, err := data.GetOrderInfoByTradeId(order.TradeId)
		if err != nil {
			t.Fatal(err)
		}
		callbacks, err := data.GetPendingCallbackOrders(3, 10)
		if err != nil {
			t.Fatal(err)
		}
		if phase != 2 && (got.Status != mdb.StatusConfirming || got.BlockTransactionId != "" || len(callbacks) != 0) {
			t.Fatal("unverified credit", got, callbacks)
		}
		if phase == 2 && (got.Status != mdb.StatusPaySuccess || got.BlockTransactionId != incoming || len(callbacks) != 1) {
			t.Fatal("verified credit missing", got, callbacks)
		}
	}
	if counts[0].Load() != 3 || counts[1].Load() != 3 {
		t.Fatal("strict requests", counts[0].Load(), counts[1].Load())
	}
	if err := service.ConfirmPayment(ctx, client, remote.full, job); err == nil {
		t.Fatal("settled twice")
	}

}

func TestNetworkProtocolPairingAndRecurringProbe(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	nodes := []mdb.RpcNode{
		{Network: "solana", Type: "ws", Url: "wss://node.example", ApiKey: "stream", Weight: 80},
		{Network: "solana", Type: "http", Url: "https://history.example", ApiKey: "history", Weight: 50},
		{Network: "solana", Type: "http", Url: "https://manual.example", Purpose: mdb.RpcNodePurposeManualVerify, Weight: 1000},
	}
	for i := range nodes {
		nodes[i].Enabled = true
		if e := data.CreateRpcNode(&nodes[i]); e != nil {
			t.Fatal(e)
		}
	}
	rows, e := data.GeneralScannerNodes("solana", scanner.Protocols("solana"))
	if e != nil {
		t.Fatal(e)
	}
	n, ok := scannerNodeConfig(nodes[0], rows)
	if !ok || n.HistoryURL != nodes[1].Url || n.HistoryAPIKey != "history" || n.APIKey != "stream" {
		t.Fatal("incorrect historical node selection")
	}
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"chainID":"0x2105","block":{"number":"0x10"}}}`)
	}))
	defer gql.Close()
	status, _, e := probeReachability(mdb.RpcNode{Network: "base", Type: "graphql", Url: gql.URL})
	if e != nil || status != mdb.RpcNodeStatusOk {
		t.Fatal(status, e)
	}
}
