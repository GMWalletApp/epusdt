package service

import (
	"strings"

	"github.com/GMWalletApp/epusdt/model/mdb"
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
