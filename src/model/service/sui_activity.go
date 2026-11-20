package service

import (
	"context"
	"errors"
	"math/big"
	"strings"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/tidwall/gjson"
)

func isNativeEVMToken(network string, t mdb.ChainToken) bool {
	symbol := scanner.NativeAsset(network)
	if symbol == "" || !strings.EqualFold(symbol, t.Symbol) {
		return false
	}
	return t.ContractAddress == "" || (network == mdb.NetworkArc && strings.EqualFold(t.ContractAddress, "0x3600000000000000000000000000000000000000"))
}

// Move identifiers are case sensitive. Normalize just the package address.
func suiCoinType(value string) string {
	pkg, rest, ok := strings.Cut(strings.TrimSpace(value), "::")
	if !ok || rest == "" {
		return ""
	}
	canonical, err := scanner.Canonical("sui", pkg)
	if err != nil {
		return ""
	}
	return canonical + "::" + rest
}
func suiCredit(e scanner.Activity, address string, t mdb.ChainToken) *big.Int {
	tx := gjson.ParseBytes(e.Data)
	if tx.Get("digest").String() != e.TxID || !tx.Get("effects.status.success").Bool() {
		return nil
	}
	to, err := scanner.Canonical("sui", address)
	if err != nil {
		return nil
	}
	sender, _ := scanner.Canonical("sui", tx.Get("transaction.sender").String())
	if sender == to {
		return nil
	}
	coin := suiCoinType(t.ContractAddress)
	if coin == "" {
		return nil
	}
	var credit *big.Int
	debit := false
	for _, change := range tx.Get("balance_changes").Array() {
		if suiCoinType(change.Get("coin_type").String()) != coin {
			continue
		}
		owner, err := scanner.Canonical("sui", change.Get("address").String())
		if err != nil {
			continue
		}
		v, ok := new(big.Int).SetString(change.Get("amount").String(), 10)
		if !ok {
			continue
		}
		if owner == to && v.Sign() > 0 {
			credit = v
		}
		if owner != to && v.Sign() < 0 {
			debit = true
		}
	}
	if !debit {
		return nil
	}
	return credit
}
func validateManualSui(order *mdb.Orders, digest string) (string, error) {
	token, err := data.GetEnabledChainTokenBySymbol(order.Network, order.Token)
	if err != nil {
		return "", err
	}
	if token == nil || token.ID == 0 {
		return "", errors.New("missing enabled Sui coin type")
	}
	chain, err := data.GetChainByNetwork(order.Network)
	if err != nil {
		return "", err
	}
	nodes, err := data.ListManualPaymentRpcCandidates(order.Network, mdb.RpcNodeTypeHttp)
	if err != nil {
		return "", err
	}
	last := errors.New("no Sui RPC node available")
	for _, node := range nodes {
		ctx, cancel := context.WithTimeout(context.Background(), manualVerifyRequestTimeout)
		e, err := scanner.SuiTransaction(ctx, scanner.Network{Name: "sui", URL: node.Url, APIKey: node.ApiKey}, digest)
		cancel()
		if err != nil {
			last = err
			continue
		}
		if e.TimeMS < order.CreatedAt.TimestampMilli() || e.Confirmations < int64(chain.MinConfirmations) || !amountMatchesRaw(order.ActualAmount, suiCredit(e, order.ReceiveAddress, *token), token.Decimals) {
			return "", errors.New("Sui credit does not match order")
		}
		return e.TxID, nil
	}
	return "", last
}
