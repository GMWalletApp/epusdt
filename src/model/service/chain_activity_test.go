package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/gagliardetto/solana-go"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

func TestLedgerEvidencePaysOnlyIncomingOrder(t *testing.T) {
	for _, network := range []string{"tron", "solana", "aptos", "ton"} {
		t.Run(network, func(t *testing.T) {
			defer testutil.SetupTestDatabases(t)()
			chain := mdb.Chain{Network: network, DisplayName: network, Enabled: true, MinConfirmations: 1}
			if err := dao.Mdb.Where("network = ?", network).Assign(map[string]any{"enabled": true}).FirstOrCreate(&chain).Error; err != nil {
				t.Fatal(err)
			}

			e := scanner.Activity{Network: network, TxID: "0x1234", Height: 10, Confirmations: 10000, TimeMS: time.Now().Add(2 * time.Second).Truncate(time.Second).UnixMilli()}
			token := mdb.ChainToken{Network: network, Decimals: 6, Enabled: true}
			var recipient string
			marshal := func(v any) json.RawMessage {
				b, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			switch network {
			case "tron":
				recipient, _ = scanner.Canonical(network, "411111111111111111111111111111111111111111")
				token.Symbol = "TRX"
				e.Data = marshal(scanner.Evidence{Transaction: json.RawMessage(`{"txID":"0x1234","ret":[{"contractRet":"SUCCESS"}],"raw_data":{"contract":[{"type":"TransferContract","parameter":{"value":{"amount":1000000,"to_address":"411111111111111111111111111111111111111111"}}}]}}`), Receipt: json.RawMessage(`{"id":"0x1234"}`)})
			case "solana":
				recipient = "So11111111111111111111111111111111111111112"
				token.Symbol = "SOL"
				token.Decimals = 9
				e.Data = json.RawMessage(fmt.Sprintf(`{"meta":{"err":null},"transaction":{"signatures":["0x1234"],"message":{"instructions":[{"programId":"11111111111111111111111111111111","parsed":{"type":"transfer","info":{"destination":%q,"lamports":1000000000}}}]}}}`, recipient))
			case "aptos":
				recipient, _ = scanner.Canonical(network, "0xab")
				token.Symbol = "USDT"
				token.ContractAddress = "0x23"
				e.Data = json.RawMessage(fmt.Sprintf(`{"type":"user_transaction","success":true,"timestamp":"%d","version":"10","hash":"0x1234","events":[{"type":"0x1::fungible_asset::Withdraw","data":{"amount":"1000000","store":"0x1a"}},{"type":"0x1::fungible_asset::Deposit","data":{"amount":"1000000","store":"0x1b"}}],"changes":[{"address":"0x1a","type":"write_resource","data":{"type":"0x1::fungible_asset::FungibleStore","data":{"metadata":{"inner":"0x23"}}}},{"address":"0x1b","type":"write_resource","data":{"type":"0x1::fungible_asset::FungibleStore","data":{"metadata":{"inner":"0x23"}}}},{"address":"0x1b","type":"write_resource","data":{"type":"0x1::object::ObjectCore","data":{"owner":%q}}}]}`, e.TimeMS*1000, recipient))
			case "ton":
				owner := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
				recipient = owner.StringRaw()
				token.Symbol = "TON"
				token.Decimals = 9
				tx := tonTestInboundTx(t, address.NewAddress(0, 0, bytes.Repeat([]byte{2}, 32)), owner, tlb.MustFromTON("1"), cell.BeginCell().EndCell())
				tx.Now = uint32(e.TimeMS / 1000)
				tx.AccountAddr = owner.Data()
				tx.PrevTxHash = make([]byte, 32)
				tx.StateUpdate = tlb.HashUpdate{OldHash: make([]byte, 32), NewHash: make([]byte, 32)}
				tx.OrigStatus = tlb.AccountStatusActive
				tx.EndStatus = tlb.AccountStatusActive
				tx.Description = tlb.TransactionDescriptionOrdinary{ComputePhase: tlb.ComputePhase{Phase: tlb.ComputePhaseSkipped{Reason: tlb.ComputeSkipReason{Type: tlb.ComputeSkipReasonNoGas}}}}
				c, err := tlb.ToCell(tx)
				if err != nil {
					t.Fatal(err)
				}
				e.TxID = hex.EncodeToString(c.Hash())
				e.Data = marshal(scanner.TONData{Account: recipient, BOC: c.ToBOC()})
			}
			if err := dao.Mdb.Where("network = ? AND symbol = ?", network, token.Symbol).Assign(token).FirstOrCreate(&token).Error; err != nil {
				t.Fatal(err)
			}
			order := mdb.Orders{TradeId: "trade", OrderId: "order", Network: network, Token: token.Symbol, Amount: 1, ActualAmount: 1, ReceiveAddress: normalizeOrderAddressByNetwork(network, recipient), Status: mdb.StatusWaitPay}
			if err := dao.Mdb.Create(&order).Error; err != nil {
				t.Fatal(err)
			}
			if err := data.LockTransaction(network, order.ReceiveAddress, order.Token, order.TradeId, 1, time.Minute); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				addresses []string
				status    int
			}{{[]string{"unrelated"}, mdb.StatusWaitPay}, {[]string{recipient}, mdb.StatusConfirming}, {[]string{recipient}, mdb.StatusConfirming}} {
				e.Addresses = tc.addresses
				if err := ApplyChainActivity(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				o, err := data.GetOrderInfoByTradeId(order.TradeId)
				if err != nil || o.Status != tc.status {
					t.Fatalf("status %d want %d, err %v", o.Status, tc.status, err)
				}
			}
			var job mdb.PaymentConfirmation
			if err := dao.Mdb.Take(&job).Error; err != nil {
				t.Fatal(err)
			}
			var result json.RawMessage
			switch network {
			case "tron":
				var ev scanner.Evidence
				if err := json.Unmarshal(e.Data, &ev); err != nil {
					t.Fatal(err)
				}
				ev.Receipt = marshal(map[string]any{"id": e.TxID, "blockNumber": 10, "blockTimeStamp": e.TimeMS})
				result = marshal(ev)
			case "aptos":
				result = e.Data
			case "solana":
				instruction := make([]byte, 12)
				binary.LittleEndian.PutUint32(instruction, 2)
				binary.LittleEndian.PutUint64(instruction[4:], 1000000000)
				result = marshal(map[string]any{"slot": 10, "blockTime": e.TimeMS / 1000, "meta": map[string]any{"err": nil}, "transaction": map[string]any{"signatures": []string{e.TxID}, "message": map[string]any{"accountKeys": []string{SystemProgramID, "sender", recipient}, "instructions": []any{map[string]any{"programIdIndex": 0, "accounts": []int{1, 2}, "data": solana.Base58(instruction)}}}}})
			case "ton":
				var d scanner.TONData
				if err := json.Unmarshal(e.Data, &d); err != nil {
					t.Fatal(err)
				}
				c, err := cell.FromBOC(d.BOC)
				if err != nil {
					t.Fatal(err)
				}
				var tx tlb.Transaction
				if err = tlb.LoadFromCell(&tx, c.BeginParse()); err != nil {
					t.Fatal(err)
				}
				result = marshal(map[string]any{"hash": base64.StdEncoding.EncodeToString(c.Hash()), "account": recipient, "lt": tx.LT, "now": tx.Now, "mc_block_seqno": 10})
			}
			if err := ConfirmPayment(t.Context(), nil, func(context.Context, string, string, int64) (json.RawMessage, error) { return result, nil }, job); err != nil {
				t.Fatal(err)
			}
			paid, err := data.GetOrderInfoByTradeId(order.TradeId)
			if err != nil || paid.Status != mdb.StatusPaySuccess || paid.BlockTransactionId == "" {
				t.Fatal(paid, err)
			}

		})
	}
}
