package service

import (
	"strings"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/shopspring/decimal"
)

func ChainTokenReadyForPayment(token mdb.ChainToken) bool {
	if strings.TrimSpace(token.Symbol) == "" {
		return false
	}
	if chainTokenRequiresAssetID(token) && strings.TrimSpace(token.ContractAddress) == "" {
		return false
	}
	return true
}

func chainTokenRequiresAssetID(token mdb.ChainToken) bool {
	network := strings.ToLower(strings.TrimSpace(token.Network))
	symbol := strings.ToUpper(strings.TrimSpace(token.Symbol))
	if native := scanner.NativeAsset(network); native != "" {
		return symbol != native
	}
	switch network {
	case mdb.NetworkTron:
		return symbol != "TRX"
	case mdb.NetworkSolana:
		return symbol != "SOL"
	case mdb.NetworkTon:
		return symbol != TonNativeSymbol
	default:
		return true
	}
}

// OnChainTokenAmount prices a new on-chain target using the same minimum
// for checkout discovery, order creation and switching.
func OnChainTokenAmount(amount float64, currency, token string) (decimal.Decimal, error) {
	rate := config.GetRateForCoin(strings.ToLower(token), strings.ToLower(currency))
	if rate <= 0 {
		return decimal.Zero, constant.RateAmountErr
	}
	quoted := decimal.NewFromFloat(amount).Mul(decimal.NewFromFloat(rate))
	if quoted.LessThan(decimal.NewFromFloat(UsdtMinimumPaymentAmount)) {
		return decimal.Zero, constant.PayAmountErr
	}
	return quoted, nil
}
