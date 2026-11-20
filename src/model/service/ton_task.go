package service

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/GMWalletApp/epusdt/util/math"
	"github.com/shopspring/decimal"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton/jetton"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

const (
	TonNativeSymbol               = "TON"
	TonTransferNotificationOpcode = 0x7362d09c
)

type TonObservedTransfer struct {
	ReceiveAddress string
	Token          mdb.ChainToken
	RawAmount      *big.Int
	Amount         float64
	BlockTimeMs    int64
	LT             uint64
	TxHashHex      string
	BlockID        string
	SenderAddress  string
	JettonWallet   string
}

func TonCanonicalBlockTransactionID(receiveRaw string, lt uint64, txHashHex string) string {
	return fmt.Sprintf("ton:%s:%d:%s", strings.ToLower(strings.TrimSpace(receiveRaw)), lt, strings.ToLower(strings.TrimSpace(txHashHex)))
}

func ParseTonInboundTransfer(tx *tlb.Transaction, receive *address.Address, nativeToken *mdb.ChainToken, jettonWalletTokens map[string]mdb.ChainToken) (*TonObservedTransfer, error) {
	if tx == nil || receive == nil || tx.IO.In == nil || tx.IO.In.MsgType != tlb.MsgTypeInternal {
		return nil, nil
	}
	if isTonTransactionBounced(tx) {
		return nil, nil
	}
	in := tx.IO.In.AsInternal()
	if in == nil || in.DstAddr == nil || !in.DstAddr.Equals(receive) {
		return nil, nil
	}

	receiveRaw := addressutil.TonRawAddressObjectKey(receive)
	txHashHex := hex.EncodeToString(tx.Hash)
	blockID := TonCanonicalBlockTransactionID(receiveRaw, tx.LT, txHashHex)
	blockTimeMs := int64(tx.Now) * 1000
	if in.SrcAddr != nil {
		srcRaw := addressutil.TonRawAddressObjectKey(in.SrcAddr)
		if token, ok := jettonWalletTokens[srcRaw]; ok {
			transfer, err := parseTonJettonTransferNotification(in)
			if err != nil {
				return nil, nil
			}
			amount := tonRawAmountToFloat(transfer.Amount.Nano(), token.Decimals)
			if amount <= 0 {
				return nil, nil
			}
			if token.MinAmount > 0 && amount < token.MinAmount {
				return nil, nil
			}
			return &TonObservedTransfer{
				ReceiveAddress: addressutil.NormalizeTonAddressObject(receive),
				Token:          token,
				RawAmount:      transfer.Amount.Nano(),
				Amount:         amount,
				BlockTimeMs:    blockTimeMs,
				LT:             tx.LT,
				TxHashHex:      txHashHex,
				BlockID:        blockID,
				SenderAddress:  addressutil.NormalizeTonAddressObject(transfer.Sender),
				JettonWallet:   addressutil.NormalizeTonAddressObject(in.SrcAddr),
			}, nil
		}
	}

	if tonPayloadIsTransferNotification(in.Body) {
		return nil, nil
	}
	if nativeToken == nil || nativeToken.ID == 0 {
		return nil, nil
	}
	rawNano := in.Amount.Nano()
	if rawNano == nil || rawNano.Sign() <= 0 {
		return nil, nil
	}
	amount := tonRawAmountToFloat(rawNano, nativeToken.Decimals)
	if amount <= 0 {
		return nil, nil
	}
	if nativeToken.MinAmount > 0 && amount < nativeToken.MinAmount {
		return nil, nil
	}
	return &TonObservedTransfer{
		ReceiveAddress: addressutil.NormalizeTonAddressObject(receive),
		Token:          *nativeToken,
		RawAmount:      rawNano,
		Amount:         amount,
		BlockTimeMs:    blockTimeMs,
		LT:             tx.LT,
		TxHashHex:      txHashHex,
		BlockID:        blockID,
	}, nil
}

func tonOrderAddressMatches(a, b string) bool {
	ar, errA := addressutil.TonRawAddressKey(a)
	br, errB := addressutil.TonRawAddressKey(b)
	return errA == nil && errB == nil && ar == br
}

func tonRawAmountToFloat(rawAmount *big.Int, decimals int) float64 {
	if rawAmount == nil || rawAmount.Sign() <= 0 {
		return 0
	}
	if decimals < 0 {
		decimals = 0
	}
	amount := decimal.NewFromBigInt(rawAmount, -int32(decimals))
	return math.MustParsePrecFloat64(amount.InexactFloat64(), data.MaxAmountPrecision)
}

func parseTonJettonTransferNotification(in *tlb.InternalMessage) (*jetton.TransferNotification, error) {
	if in == nil || in.Body == nil {
		return nil, fmt.Errorf("missing jetton transfer notification body")
	}
	var transfer jetton.TransferNotification
	if err := tlb.LoadFromCell(&transfer, in.Body.BeginParse()); err != nil {
		return nil, err
	}
	return &transfer, nil
}

func tonPayloadIsTransferNotification(body *cell.Cell) bool {
	if body == nil {
		return false
	}
	op, err := body.BeginParse().LoadUInt(32)
	return err == nil && op == TonTransferNotificationOpcode
}

func isTonTransactionBounced(tx *tlb.Transaction) bool {
	if tx == nil {
		return false
	}
	if dsc, ok := tx.Description.(tlb.TransactionDescriptionOrdinary); ok && dsc.BouncePhase != nil {
		if _, ok = dsc.BouncePhase.Phase.(tlb.BouncePhaseOk); ok {
			return true
		}
	}
	return false
}

func EnsureTonTransferMatchesOrder(order *mdb.Orders, transfer *TonObservedTransfer) error {
	if order == nil || order.ID == 0 {
		return fmt.Errorf("order not found")
	}
	if transfer == nil {
		return fmt.Errorf("matching ton transfer to order address not found")
	}
	if strings.ToLower(strings.TrimSpace(order.Network)) != mdb.NetworkTon {
		return fmt.Errorf("order network mismatch")
	}
	if !strings.EqualFold(order.Token, transfer.Token.Symbol) {
		return fmt.Errorf("transaction token mismatch")
	}
	if !tonOrderAddressMatches(order.ReceiveAddress, transfer.ReceiveAddress) {
		return fmt.Errorf("transaction recipient mismatch")
	}
	if transfer.BlockTimeMs <= 0 || transfer.BlockTimeMs < order.CreatedAt.TimestampMilli() {
		return fmt.Errorf("transaction predates the order")
	}
	if !amountMatchesRaw(order.ActualAmount, transfer.RawAmount, transfer.Token.Decimals) {
		return fmt.Errorf("transaction amount mismatch")
	}
	return nil
}
