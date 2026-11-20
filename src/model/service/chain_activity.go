package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/tidwall/gjson"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton/jetton"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

const transferSignature = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// ApplyChainActivity interprets confirmed evidence and returns transient storage
// and lookup errors to the queue so acknowledgement can follow durable settlement.
func ApplyChainActivity(ctx context.Context, e scanner.Activity) error {
	if e.TimeMS <= 0 || e.TxID == "" || len(e.Addresses) == 0 {
		return nil
	}
	tokens, err := data.ListEnabledChainTokensByNetwork(e.Network)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return nil
	}
	switch e.Network {
	case mdb.NetworkSui:
		for _, t := range tokens {
			for _, a := range e.Addresses {
				if raw := suiCredit(e, a, t); raw != nil {
					if err := settleActivity(e, a, t, raw, e.TxID); err != nil {
						return err
					}
				}
			}
		}
		return nil
	case mdb.NetworkAptos:
		wallets := map[string]struct{}{}
		for _, a := range e.Addresses {
			wallets[a] = struct{}{}
		}
		batch, err := json.Marshal([]json.RawMessage{e.Data})
		if err != nil {
			return err
		}
		transfers, err := ParseAptosTransfersForWallets(batch, wallets, tokens)
		if err != nil {
			return err
		}
		for _, t := range transfers {
			token := mdb.ChainToken{Symbol: t.Token, Decimals: t.Decimals, MinAmount: t.MinAmount}
			if err = settleActivity(e, t.ReceiveAddress, token, t.RawAmount, t.TxID); err != nil {
				return err
			}
		}
		return nil
	case mdb.NetworkTon:
		return applyTONActivity(ctx, e, tokens)
	case mdb.NetworkSolana:
		return applySolanaActivity(e, tokens)
	default:
		if scanner.IsEVM(e.Network) || e.Network == mdb.NetworkTron {
			return applyLogActivity(e, tokens)
		}
	}
	return fmt.Errorf("unknown scanner network %q", e.Network)
}

func applyLogActivity(e scanner.Activity, tokens []mdb.ChainToken) error {
	var evidence scanner.Evidence
	if err := json.Unmarshal(e.Data, &evidence); err != nil {
		return err
	}
	tx := gjson.ParseBytes(evidence.Transaction)
	byContract := map[string]mdb.ChainToken{}
	var native *mdb.ChainToken
	for i := range tokens {
		t := tokens[i]
		if isNativeEVMToken(e.Network, t) {
			v := t
			v.Decimals = 18
			native = &v
		}
		if t.ContractAddress == "" && e.Network == mdb.NetworkTron && strings.EqualFold(t.Symbol, "TRX") {
			native = &tokens[i]
			continue
		}
		if a, err := scanner.Canonical(e.Network, t.ContractAddress); err == nil {
			byContract[a] = t
		}
	}
	logs := evidence.Logs
	if e.Network == mdb.NetworkTron {
		if tx.Get("txID").String() != e.TxID || tx.Get("ret.0.contractRet").String() != "SUCCESS" {
			return nil
		}
		r := gjson.ParseBytes(evidence.Receipt)
		if r.Get("id").String() != e.TxID || r.Get("result").String() == "FAILED" {
			return nil
		}
		if native != nil {
			for _, c := range tx.Get("raw_data.contract").Array() {
				if c.Get("type").String() != "TransferContract" {
					continue
				}
				v := c.Get("parameter.value")
				a, _ := scanner.Canonical(e.Network, v.Get("to_address").String())
				raw, ok := new(big.Int).SetString(v.Get("amount").String(), 10)
				if ok {
					if err := settleActivity(e, a, *native, raw, e.TxID); err != nil {
						return err
					}
				}
			}
		}
		for _, l := range r.Get("log").Array() {
			logs = append(logs, json.RawMessage(l.Raw))
		}
	} else if tx.Get("hash").String() != e.TxID {
		return nil
	}
	if native != nil && scanner.IsEVM(e.Network) {
		r := gjson.ParseBytes(evidence.Receipt)
		raw, ok := new(big.Int).SetString(tx.Get("value").String(), 0)
		to := tx.Get("to").String()
		if ok && to != "" && r.Get("transactionHash").String() == e.TxID && r.Get("status").String() == "0x1" && !strings.EqualFold(tx.Get("from").String(), to) {
			if err := settleActivity(e, to, *native, raw, e.TxID); err != nil {
				return err
			}
		}
	}
	for _, raw := range logs {
		l := gjson.ParseBytes(raw)
		topics := l.Get("topics").Array()
		if len(topics) != 3 || strings.TrimPrefix(topics[0].String(), "0x") != transferSignature || l.Get("removed").Bool() {
			continue
		}
		contract, err := scanner.Canonical(e.Network, l.Get("address").String())
		if err != nil {
			continue
		}
		t, ok := byContract[contract]
		if !ok {
			continue
		}
		dest := strings.TrimPrefix(topics[2].String(), "0x")
		value := strings.TrimPrefix(l.Get("data").String(), "0x")
		if len(dest) != 64 || !strings.HasPrefix(dest, strings.Repeat("0", 24)) || len(value) != 64 {
			continue
		}
		a, err := scanner.Canonical(e.Network, dest[24:])
		if err != nil {
			continue
		}
		quantity, ok := new(big.Int).SetString(value, 16)
		if !ok {
			continue
		}
		if err = settleActivity(e, a, t, quantity, e.TxID); err != nil {
			return err
		}
	}
	return nil
}

func settleActivity(e scanner.Activity, recipient string, t mdb.ChainToken, raw *big.Int, blockID string) error {
	canonical, err := scanner.Canonical(e.Network, recipient)
	if err != nil || !slices.Contains(e.Addresses, canonical) || raw == nil || raw.Sign() <= 0 || t.Decimals < 0 || t.Decimals > 255 {
		return nil
	}
	recipient = normalizeOrderAddressByNetwork(e.Network, recipient)
	symbol := strings.ToUpper(strings.TrimSpace(t.Symbol))
	amount := rawToFloat(raw, t.Decimals)
	if amount <= 0 || amount < t.MinAmount {
		return nil
	}
	order, fromLock, err := resolveEvmTransferOrder(e.Network, recipient, symbol, amount, e.TimeMS)
	if err != nil {
		return err
	}
	if fromLock && order != nil && order.CreatedAt.TimestampMilli() > e.TimeMS {
		order, err = data.GetOnChainOrderByWalletAddressAndAmountAndTokenBeforeTime(e.Network, recipient, symbol, amount, time.UnixMilli(e.TimeMS))
		if err != nil {
			return err
		}
	}
	if order == nil || order.ID == 0 || order.Network != e.Network || !strings.EqualFold(order.Token, symbol) || (order.PayProvider != "" && order.PayProvider != mdb.PaymentProviderOnChain) {
		return nil
	}
	a, err := scanner.Canonical(e.Network, order.ReceiveAddress)
	if err != nil || a != canonical || !amountMatchesRaw(order.ActualAmount, raw, t.Decimals) {
		return nil
	}
	if e.TimeMS < order.CreatedAt.TimestampMilli() || e.TimeMS > order.CreatedAt.AddMinutes(config.GetOrderExpirationTime()).TimestampMilli() {
		return nil
	}
	req := &request.OrderProcessingRequest{ReceiveAddress: order.ReceiveAddress, Token: symbol, Network: e.Network, TradeId: order.TradeId, Amount: order.ActualAmount, BlockTransactionId: blockID}
	err = orderProcessingWithAllowedStatuses(req, []int{mdb.StatusWaitPay, mdb.StatusExpired})
	if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	sendPaymentNotification(order)
	return nil
}

func applySolanaActivity(e scanner.Activity, tokens []mdb.ChainToken) error {
	tx := gjson.ParseBytes(e.Data)
	if tx.Get("transaction.signatures.0").String() != e.TxID || tx.Get("meta.err").Raw != "null" {
		return nil
	}
	wrapped, err := json.Marshal(map[string]json.RawMessage{"result": e.Data})
	if err != nil {
		return err
	}
	instructions := tx.Get("transaction.message.instructions").Array()
	for _, inner := range tx.Get("meta.innerInstructions").Array() {
		instructions = append(instructions, inner.Get("instructions").Array()...)
	}
	for _, ins := range instructions {
		tr, err := ParseTransferInfoFromInstruction(ins, wrapped)
		if err != nil || tr == nil {
			continue
		}
		for _, t := range tokens {
			native := tr.Mint == "SOL" && t.ContractAddress == "" && strings.EqualFold(t.Symbol, "SOL")
			if !native && (tr.Mint == "" || t.ContractAddress != tr.Mint) {
				continue
			}
			if tr.Decimals != nil && int(*tr.Decimals) != t.Decimals {
				continue
			}
			for _, a := range e.Addresses {
				if (native && a == tr.Destination) || (!native && MatchAtaAddress(a, tr.Mint, tr.Destination)) {
					if err = settleActivity(e, a, t, new(big.Int).SetUint64(tr.RawAmount), e.TxID); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func applyTONActivity(ctx context.Context, e scanner.Activity, tokens []mdb.ChainToken) error {
	var d scanner.TONData
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return err
	}
	if !slices.Contains(e.Addresses, d.Account) {
		return nil
	}
	owner, err := address.ParseRawAddr(d.Account)
	if err != nil {
		return err
	}
	c, err := cell.FromBOC(d.BOC)
	if err != nil {
		return err
	}
	var tx tlb.Transaction
	if err = tlb.LoadFromCell(&tx, c.BeginParse()); err != nil {
		return err
	}
	tx.Hash = c.Hash()
	if hex.EncodeToString(tx.Hash) != e.TxID || tx.IO.In == nil || tx.IO.In.MsgType != tlb.MsgTypeInternal {
		return nil
	}
	var native *mdb.ChainToken
	wallets := map[string]mdb.ChainToken{}
	for i := range tokens {
		if tokens[i].ContractAddress == "" && strings.EqualFold(tokens[i].Symbol, TonNativeSymbol) {
			native = &tokens[i]
		}
	}
	if tx.IO.In.AsInternal().Bounced {
		return nil
	}
	if tonPayloadIsTransferNotification(tx.IO.In.AsInternal().Body) {
		if d.Master == nil {
			return errors.New("TON activity missing master block")
		}
		node, err := data.SelectGeneralRpcNode(mdb.NetworkTon, mdb.RpcNodeTypeLite)
		if err != nil {
			return err
		}
		if node == nil {
			return errors.New("TON Jetton verification requires a general lite node")
		}
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		api, close, err := ConnectTonLiteAPI(ctx, node.Url, 10*time.Second, 2)
		if err != nil {
			return err
		}
		defer close()
		for _, t := range tokens {
			if t.ContractAddress == "" {
				continue
			}
			master, err := scanner.Canonical("ton", t.ContractAddress)
			if err != nil {
				continue
			}
			addr, err := address.ParseRawAddr(master)
			if err != nil {
				return err
			}
			w, err := jetton.NewJettonMasterClient(api, addr).GetJettonWalletAtBlock(ctx, owner, d.Master)
			if err != nil {
				return err
			}
			wallets[w.Address().StringRaw()] = t
		}
	}
	tr, err := ParseTonInboundTransfer(&tx, owner, native, wallets)
	if err != nil || tr == nil {
		return err
	}
	return settleActivity(e, tr.ReceiveAddress, tr.Token, tr.RawAmount, tr.BlockID)
}
