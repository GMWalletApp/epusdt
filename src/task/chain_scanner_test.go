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
	api "github.com/GMWalletApp/ChainScanner/api/activity/v1"
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
	blockHash := "0x" + strings.Repeat("cd", 32)
	const wallet = "0x1111111111111111111111111111111111111111"
	const contract = "0x2222222222222222222222222222222222222222"
	if err := dao.Mdb.Model(&mdb.Chain{}).Where("network = ?", net).Update("min_confirmations", 2).Error; err != nil {
		t.Fatal(err)
	}
	receiving := &mdb.WalletAddress{Network: net, Address: wallet}
	for _, row := range []any{receiving, &mdb.ChainToken{Network: net, Symbol: "FOO", ContractAddress: contract, Decimals: 6, Enabled: true}} {
		if err := dao.Mdb.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	order := mdb.Orders{TradeId: "scanner-payment", OrderId: "merchant-order", Network: net, Token: "FOO", Amount: 1, ActualAmount: 1, ReceiveAddress: wallet, Status: mdb.StatusWaitPay}
	// Production orders store created_at in UTC; the scanner matches the
	// payment against that UTC timestamp.
	if err := data.CreateOrderWithTransaction(dao.Mdb, &order); err != nil {
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
	// An outstanding invoice must still settle after both admin operations
	// and after the gateway rebuilds its subscription from an empty cache.
	if err := data.ChangeWalletAddressStatus(receiving.ID, mdb.TokenStatusDisable); err != nil {
		t.Fatal(err)
	}
	if err := sub.refresh(ctx); err != nil || !sub.registered[scanner.Watch{Network: net, Address: wallet}] {
		t.Fatalf("disabled wallet lost its pending payment watch: %v", err)
	}
	if err := data.DeleteWalletAddressById(receiving.ID); err != nil {
		t.Fatal(err)
	}
	sub = paymentSubscriptions{feed: client}
	if err := sub.refresh(ctx); err != nil || !sub.registered[scanner.Watch{Network: net, Address: wallet}] {
		t.Fatalf("removed wallet lost its pending payment watch after restart: %v", err)
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
			result = map[string]any{"number": "0x11", "hash": blockHash, "parentHash": "parent", "timestamp": fmt.Sprintf("0x%x", time.Now().Unix()+1), "transactions": []any{map[string]any{"hash": outgoing, "from": wallet, "to": contract}, map[string]any{"hash": incoming, "from": contract, "to": wallet}}}
		case "eth_getLogs":
			var logs []any
			for i, id := range []string{outgoing, incoming} {
				recipient := contract
				if i == 1 {
					recipient = wallet
				}
				logs = append(logs, map[string]any{"blockHash": blockHash, "transactionHash": id, "address": contract, "topics": []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", "0x" + strings.Repeat("0", 64), "0x" + strings.Repeat("0", 24) + recipient[2:]}, "data": "0x" + amounts[i]})
			}
			result = logs
		default:
			t.Error(req.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer rpc.Close()
	// Only the external scanner fixture reads blocks. EPUSDT consumes its feed.
	cfg := scanner.Network{Name: net, Protocol: "http", URL: rpc.URL, Confirmations: 2}
	source, err := scanner.NewSource(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err = store.Pump(ctx, cfg, source); err != nil {
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
	remote := newTransactionClient(scanner.Client{URL: api.URL, Token: token})
	// A scan source with a higher weight must still be excluded after ACK.
	if _, err := registry.Add(ctx, scanner.NodeOptions{Weight: 100, NodeEndpoint: scanner.NodeEndpoint{Network: net, Protocol: "http", URL: rpc.URL}}); err != nil {
		t.Fatal(err)
	}
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
				if mode.Load() == 1 {
					break
				}
				result = map[string]any{"hash": incoming, "from": contract, "to": wallet, "input": "0x", "value": "0x0", "blockHash": blockHash, "blockNumber": "0x11"}
			case "eth_getTransactionReceipt":
				amount := amounts[1]
				if mode.Load() == 0 {
					amount = amounts[0]
				}
				result = map[string]any{"transactionHash": incoming, "blockHash": blockHash, "blockNumber": "0x11", "status": "0x1", "logs": []any{map[string]any{"address": contract, "transactionHash": incoming, "blockHash": blockHash, "data": "0x" + amount, "topics": []string{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", "0x" + strings.Repeat("0", 64), "0x" + strings.Repeat("0", 24) + wallet[2:]}}}}
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
	for _, phase := range []int32{1, 0} {
		mode.Store(phase)
		lookup := remote
		if phase == 1 {
			if err := service.ConfirmPayment(ctx, client, lookup.automatic, job); err == nil {
				t.Fatalf("phase %d accepted unverified transfer", phase)
			}
			expired, err := data.UpdateOrderIsExpirationById(order.ID, time.Now().Add(time.Hour))
			if err != nil || expired {
				t.Fatal("expired confirming payment", err)
			}
		} else {
			// The second RPC reports a different amount. Existence completes the recheck;
			// the worker settles from the previously matched scan event.
			run, cancel := context.WithTimeout(ctx, 5*time.Second)
			stopped := make(chan struct{})
			go func() { defer close(stopped); service.RunPaymentConfirmations(run, client, lookup.automatic) }()
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
		if phase == 1 && (got.Status != mdb.StatusConfirming || got.BlockTransactionId != "" || len(callbacks) != 0) {
			t.Fatal("unverified credit", got, callbacks)
		}
		if phase == 0 && (got.Status != mdb.StatusPaySuccess || got.BlockTransactionId != incoming || len(callbacks) != 1) {
			t.Fatal("verified credit missing", got, callbacks)
		}
	}
	if counts[0].Load()+counts[1].Load() != 5 {
		t.Fatal("lookup requests", counts[0].Load(), counts[1].Load())
	}
	if err := service.ConfirmPayment(ctx, client, remote.automatic, job); err == nil {
		t.Fatal("settled twice")
	}
	if err := sub.refresh(ctx); err != nil || len(sub.registered) != 0 {
		t.Fatalf("wallet subscription after final settlement: %v, %v", sub.registered, err)
	}
}

func TestManualNodeProtocolPairingAndProbe(t *testing.T) {
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
	status, _, e := probeRpcNode(mdb.RpcNode{Network: "base", Type: "graphql", Url: gql.URL})
	if e != nil || status != mdb.RpcNodeStatusOk {
		t.Fatal(status, e)
	}
}

type eventSource struct{ rows []scanner.Activity }

func (s eventSource) Head(context.Context) (int64, error) { return 2, nil }
func (s eventSource) Read(_ context.Context, _, end int64) (int64, []scanner.Activity, error) {
	return end, s.rows, nil
}
func (eventSource) Close() {}

func TestRemotePushAcknowledgesCommittedActivity(t *testing.T) {
	for _, transport := range []string{"ws", "grpc"} {
		t.Run(transport, func(t *testing.T) {
			defer testutil.SetupTestDatabases(t)()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			const network = "ethereum"
			const addr = "0x1111111111111111111111111111111111111111"
			store, err := scanner.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.SeedCursor(ctx, network, 1); err != nil {
				t.Fatal(err)
			}
			token := strings.Repeat("c", 32)
			handler, err := scanner.Handler(store, map[string]string{paymentConsumer: token})
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewUnstartedServer(handler)
			protocols := new(http.Protocols)
			protocols.SetHTTP1(true)
			protocols.SetUnencryptedHTTP2(true)
			srv.Config.Protocols = protocols
			srv.Start()
			defer srv.Close()
			c := scanner.Client{URL: srv.URL, Token: token}
			if _, err := c.Subscribe(ctx, paymentConsumer, []scanner.Watch{{Network: network, Address: addr}}); err != nil {
				t.Fatal(err)
			}
			source := eventSource{[]scanner.Activity{{Network: network, Height: 2, TxID: "unmatched", TimeMS: time.Now().UnixMilli(), Addresses: []string{addr}, Data: json.RawMessage("{}")}}}
			if _, err := store.Step(ctx, scanner.Network{Name: network}, source); err != nil {
				t.Fatal(err)
			}
			next := func() *scanner.Delivery {
				d, err := store.PendingNetwork(ctx, paymentConsumer, network, 0)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			if next() == nil {
				t.Fatal("fixture did not enqueue")
			}
			done := make(chan error, 1)
			go func() {
				if transport == "ws" {
					done <- websocketFeed(ctx, c, network)
				} else {
					done <- grpcFeed(ctx, c, network)
				}
			}()
			defer func() { cancel(); <-done }()
			for next() != nil {
				select {
				case <-ctx.Done():
					t.Fatal("delivery not acknowledged")
				case <-time.After(10 * time.Millisecond):
				}
			}
		})
	}
}

func TestStreamLeavesFailedDeliveryUnacknowledged(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	frames := []*api.ServerFrame{
		{Result: &api.ServerFrame_Ready{Ready: &api.Ready{}}},
		{Result: &api.ServerFrame_Delivery{Delivery: &api.Delivery{Id: 1, Network: "ethereum", TxId: "bad", TimeMs: 1, Addresses: []string{"address"}, DataJson: "not json"}}},
	}
	acked := false
	err := readFrames(t.Context(), "ethereum", func(f *api.ClientFrame) error {
		if f.GetAck() != 0 {
			acked = true
		}
		return nil
	}, func() (*api.ServerFrame, error) {
		if len(frames) == 0 {
			t.Fatal("read after failed event")
		}
		f := frames[0]
		frames = frames[1:]
		return f, nil
	})
	if err == nil || acked {
		t.Fatalf("err=%v ack=%v", err, acked)
	}
}

func TestPaymentWatchesRetainOutstandingOnChainOrders(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	ctx := context.Background()
	net := mdb.NetworkEthereum
	primary := "0x" + strings.Repeat("ab", 20)
	child := "0x" + strings.Repeat("cd", 20)
	other := "0x" + strings.Repeat("ef", 20)
	if _, err := data.AddWalletAddressWithNetwork(net, primary); err != nil {
		t.Fatal(err)
	}
	// Use orders without a current wallet row to cover previously deleted
	// wallets, including legacy rows with NULL pay_provider.
	for _, invoice := range []struct {
		id, parent, addr, provider string
		status                     int
	}{
		{"parent", "", strings.ToUpper(primary[2:]), "", mdb.StatusWaitPay},
		{"child", "parent", child, "", mdb.StatusConfirming},
		{"second", "", child, "", mdb.StatusWaitPay},
		{"paid", "", other, "", mdb.StatusPaySuccess},
		{"expired", "", other, "", mdb.StatusExpired},
		{"review", "", other, "", mdb.StatusManualReview},
		{"placeholder", "", other, "", mdb.StatusWaitSelect},
		{"hosted", "", other, mdb.PaymentProviderOkPay, mdb.StatusWaitPay},
	} {
		row := mdb.Orders{TradeId: invoice.id, OrderId: invoice.id, ParentTradeId: invoice.parent,
			Status: invoice.status, PayProvider: invoice.provider, ReceiveAddress: invoice.addr, Network: net, Token: "FOO", ActualAmount: 1}
		if err := dao.Mdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := dao.Mdb.Model(&mdb.Orders{}).Where("trade_id IN ?", []string{"second", "child"}).Update("pay_provider", nil).Error; err != nil {
		t.Fatal(err)
	}
	store, err := scanner.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scanner.ConfigureNetworks(store, map[string]scanner.Network{net: {Name: net}}, func(context.Context, string) (int64, error) { return 10, nil })
	if err := store.SeedCursor(ctx, net, 10); err != nil {
		t.Fatal(err)
	}
	sub := paymentSubscriptions{feed: store}
	check := func(want ...string) {
		t.Helper()
		if err := sub.refresh(ctx); err != nil {
			t.Fatal(err)
		}
		if len(sub.registered) != len(want) {
			t.Fatalf("registered %v, want %v", sub.registered, want)
		}
		for _, addr := range want {
			if !sub.registered[scanner.Watch{Network: net, Address: addr}] {
				t.Fatalf("missing %s in %v", addr, sub.registered)
			}
		}
	}
	check(primary, child)
	if err := dao.Mdb.Model(&mdb.Orders{}).Where("trade_id = ?", "second").Update("status", mdb.StatusExpired).Error; err != nil {
		t.Fatal(err)
	}
	check(primary, child)
	if err := dao.Mdb.Model(&mdb.Orders{}).Where("trade_id = ?", "child").Update("status", mdb.StatusManualReview).Error; err != nil {
		t.Fatal(err)
	}
	check(primary)
}
