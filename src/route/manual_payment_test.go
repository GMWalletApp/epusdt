package route

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/scanneradmin"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/labstack/echo/v4"
)

func TestSubmitTxHashRequiresPaymentChecksAndLookupRecheck(t *testing.T) {
	for _, tc := range []struct {
		name          string
		predates      bool
		confirmations int64
		lookupFails   bool
		wantPaid      bool
	}{
		{name: "historical transfer", predates: true, confirmations: 12},
		{name: "insufficient confirmations", confirmations: 1},
		{name: "exact confirmation threshold", confirmations: 12, wantPaid: true},
		{name: "lookup unavailable", confirmations: 12, lookupFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer testutil.SetupTestDatabases(t)()
			if err := dao.Mdb.Model(&mdb.Chain{}).Where("network = ?", "ethereum").Update("min_confirmations", 12).Error; err != nil {
				t.Fatal(err)
			}
			const contract = "0xdac17f958d2ee523a2206206994597c13d831ec7"
			const recipient = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
			const sender = "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"
			hash := "0x" + strings.Repeat("a", 64)
			stamp := time.Now().Unix()
			if tc.predates {
				stamp -= 24 * 60 * 60
			}
			var head atomic.Int64
			head.Store(100 + tc.confirmations - 1)
			newRPC := func(disagree bool) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						ID     json.RawMessage
						Method string
						Params []json.RawMessage
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						return
					}
					header := &types.Header{Number: big.NewInt(100), Time: uint64(stamp), Difficulty: big.NewInt(1), Extra: []byte{}}
					block := header.Hash()
					var result any
					switch req.Method {
					case "eth_chainId":
						result = "0x1"
					case "eth_blockNumber":
						result = fmt.Sprintf("0x%x", head.Load())
					case "eth_getBlockByNumber":
						if string(req.Params[0]) == `"latest"` {
							header.Number = big.NewInt(head.Load())
						}
						result = header
					case "eth_getTransactionReceipt":
						result = &types.Receipt{Status: 1, BlockNumber: big.NewInt(100), BlockHash: block, TxHash: common.HexToHash(hash), Logs: []*types.Log{{
							Address: common.HexToAddress(contract), Topics: []common.Hash{common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"), common.HexToHash(sender), common.HexToHash(recipient)},
							Data: common.LeftPadBytes(big.NewInt(100000).Bytes(), 32), BlockHash: block, TxHash: common.HexToHash(hash), BlockNumber: 100,
						}}}
					case "eth_getTransactionByHash":
						from := sender
						if disagree {
							from = recipient
						}
						result = map[string]any{"hash": hash, "blockHash": block.Hex(), "blockNumber": "0x64", "from": from,
							"to": contract, "input": "0x", "value": "0x0"}
					default:
						t.Errorf("unexpected RPC method %s", req.Method)
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
				}))
			}
			first, second := newRPC(false), newRPC(false)
			defer first.Close()
			defer second.Close()
			store, err := scanner.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			registry, err := scanner.NewNodeRegistry(t.Context(), store, []scanner.Network{{Name: "ethereum"}})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := registry.List(t.Context(), "ethereum")
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if err := registry.Delete(t.Context(), row.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, rpc := range []*httptest.Server{first, second} {
				node, err := registry.Add(t.Context(), scanner.NodeOptions{NodeEndpoint: scanner.NodeEndpoint{Network: "ethereum", Protocol: "http", URL: rpc.URL}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := registry.Test(t.Context(), node.ID); err != nil {
					t.Fatal(err)
				}
			}
			conn := config.ScannerConnection{AdminToken: strings.Repeat("a", 32), ConsumerToken: strings.Repeat("c", 32)}
			handler, err := registry.Handler(map[string]string{"epusdt": conn.ConsumerToken}, conn.AdminToken)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			conn.URL = server.URL
			scanneradmin.CandidateInventory = func(ctx context.Context, _ config.ScannerConnection, network, use string) (scanner.RouteInventory, error) {
				return scanneradmin.Inventory(ctx, conn, network, use)
			}
			previous := service.LookupPaymentTransaction
			t.Cleanup(func() { service.LookupPaymentTransaction = previous })
			service.LookupPaymentTransaction = func(ctx context.Context, network, hash string) (json.RawMessage, error) {
				if tc.lookupFails {
					return nil, fmt.Errorf("transaction lookup unavailable")
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/transactions/"+network+"/"+hash+"?manual=true", nil)
				if err != nil {
					return nil, err
				}
				req.Header.Set("Authorization", "Bearer "+conn.ConsumerToken)
				res, err := server.Client().Do(req)
				if err != nil {
					return nil, err
				}
				defer res.Body.Close()
				if res.StatusCode != http.StatusOK {
					return nil, fmt.Errorf("lookup HTTP %d", res.StatusCode)
				}
				var body struct{ Result json.RawMessage }
				err = json.NewDecoder(res.Body).Decode(&body)
				return body.Result, err
			}
			order := mdb.Orders{TradeId: "manual-order", OrderId: "manual-order", ActualAmount: 0.1, ReceiveAddress: recipient,
				Network: "ethereum", Token: "USDT", Status: mdb.StatusWaitPay, PayProvider: mdb.PaymentProviderOnChain}
			if err := data.CreateOrderWithTransaction(dao.Mdb, &order); err != nil {
				t.Fatal(err)
			}
			if err := dao.Mdb.Model(&order).Update("created_at", time.Now().Add(-time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			e := echo.New()
			RegisterRoute(e)
			submit := func(wantPaid bool) {
				t.Helper()
				rec := doPost(e, "/pay/submit-tx-hash/"+order.TradeId, map[string]interface{}{"block_transaction_id": hash})
				wantHTTP, wantStatus := http.StatusBadRequest, mdb.StatusWaitPay
				if wantPaid {
					wantHTTP, wantStatus = http.StatusOK, mdb.StatusPaySuccess
				}
				if rec.Code != wantHTTP {
					t.Fatalf("submit HTTP %d, want %d: %s", rec.Code, wantHTTP, rec.Body.String())
				}
				var stored mdb.Orders
				if err := dao.Mdb.First(&stored, order.ID).Error; err != nil {
					t.Fatal(err)
				}
				if stored.Status != wantStatus || (wantPaid && stored.BlockTransactionId != hash) || (!wantPaid && stored.BlockTransactionId != "") {
					t.Fatalf("status=%d transaction=%q, want status=%d", stored.Status, stored.BlockTransactionId, wantStatus)
				}
			}
			submit(tc.wantPaid)
			if tc.confirmations < 12 {
				head.Store(111)
				submit(true)
			}
		})
	}
}
