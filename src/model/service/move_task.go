package service

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
)

type MoveObservedTransfer struct {
	Network        string
	ReceiveAddress string
	Token          string
	RawAmount      *big.Int
	Decimals       int
	MinAmount      float64
	Amount         float64
	TxID           string
	BlockTimeMs    int64
	Version        int64
	TransferKey    string
}

func EnsureMoveTransferMatchesOrder(order *mdb.Orders, transfer MoveObservedTransfer) error {
	if order == nil || order.ID == 0 {
		return fmt.Errorf("order not found")
	}
	if strings.ToLower(strings.TrimSpace(order.Network)) != strings.ToLower(strings.TrimSpace(transfer.Network)) {
		return fmt.Errorf("network mismatch")
	}
	orderAddr, err := addressutil.NormalizeMoveAddress(order.ReceiveAddress)
	if err != nil {
		return fmt.Errorf("invalid order receive address: %w", err)
	}
	transferAddr, err := addressutil.NormalizeMoveAddress(transfer.ReceiveAddress)
	if err != nil {
		return fmt.Errorf("invalid transfer receive address: %w", err)
	}
	if orderAddr != transferAddr {
		return fmt.Errorf("transaction recipient mismatch")
	}
	if !strings.EqualFold(order.Token, transfer.Token) {
		return fmt.Errorf("token mismatch")
	}
	if transfer.BlockTimeMs <= 0 {
		return fmt.Errorf("transaction block time missing")
	}
	if transfer.BlockTimeMs < order.CreatedAt.TimestampMilli() {
		return fmt.Errorf("transaction predates the order")
	}
	if !amountMatchesRaw(order.ActualAmount, transfer.RawAmount, transfer.Decimals) {
		return fmt.Errorf("transaction amount mismatch")
	}
	return nil
}

func moveWalletWatched(address string, wallets map[string]struct{}) bool {
	if address == "" || len(wallets) == 0 {
		return false
	}
	_, ok := wallets[address]
	return ok
}

func moveAptosTransferKey(version int64, eventIndex int, receive, token string, raw *big.Int) string {
	rawText := ""
	if raw != nil {
		rawText = raw.String()
	}
	return fmt.Sprintf("aptos:%d:%d:%s:%s:%s", version, eventIndex, strings.ToLower(strings.TrimSpace(receive)), strings.ToUpper(strings.TrimSpace(token)), rawText)
}

func resolveMoveToken(network, assetID string, tokens []mdb.ChainToken) *mdb.ChainToken {
	assetID = addressutil.NormalizeMoveAssetID(assetID)
	for i := range tokens {
		contract := strings.TrimSpace(tokens[i].ContractAddress)
		if contract == "" {
			continue
		}
		if addressutil.MoveAssetMatches(assetID, contract) {
			return &tokens[i]
		}
	}
	return nil
}

func firstMoveAddress(values ...string) string {
	for _, v := range values {
		if normalized, err := addressutil.NormalizeMoveAddress(v); err == nil {
			return normalized
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseSignedBigInt(raw string) (*big.Int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	out, ok := new(big.Int).SetString(raw, 10)
	return out, ok
}

func rawToFloat(raw *big.Int, decimals int) float64 {
	if raw == nil {
		return 0
	}
	value, _ := new(big.Rat).SetFrac(new(big.Int).Set(raw), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)).Float64()
	return value
}

func recordMoveRPCResult(network string, errp *error) {
	if errp != nil && *errp != nil {
		data.RecordRpcFailure(network)
		return
	}
	data.RecordRpcSuccess(network)
}
