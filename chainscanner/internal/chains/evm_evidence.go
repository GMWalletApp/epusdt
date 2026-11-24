package chains

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"

	"github.com/tidwall/gjson"
)

// EVM evidence is a projection of the RPC transaction and receipt. Keep only
// fields consumed by strict payment verification: optional client extensions
// (for example blockTimestamp) must not decide whether two nodes agree.
type evmEvidence struct {
	Transaction evmTransactionEvidence `json:"transaction"`
	Receipt     *evmReceiptEvidence    `json:"receipt"`
}

type evmTransactionEvidence struct {
	Hash        string  `json:"hash"`
	BlockHash   *string `json:"blockHash"`
	BlockNumber *string `json:"blockNumber"`
	From        string  `json:"from"`
	To          *string `json:"to"`
	Input       string  `json:"input"`
	Value       string  `json:"value"`
}

type evmReceiptEvidence struct {
	TransactionHash string           `json:"transactionHash"`
	BlockHash       string           `json:"blockHash"`
	BlockNumber     string           `json:"blockNumber"`
	Status          string           `json:"status"`
	Logs            []evmLogEvidence `json:"logs"`
}

type evmLogEvidence struct {
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	TransactionHash string   `json:"transactionHash"`
	BlockHash       string   `json:"blockHash"`
	Removed         bool     `json:"removed,omitempty"`
}

func canonicalEVMEvidence(tx, receipt gjson.Result) (json.RawMessage, error) {
	txEvidence, err := canonicalEVMTransaction(tx)
	if err != nil {
		return nil, err
	}
	if receipt.Raw == "null" {
		if txEvidence.BlockHash != nil && txEvidence.BlockNumber != nil {
			return nil, ErrTransactionPending
		}
		if tx.Get("blockHash").Raw != "null" || txEvidence.BlockNumber != nil {
			return nil, ErrTransactionData
		}
		return json.Marshal(evmEvidence{Transaction: txEvidence})
	}
	receiptEvidence, err := canonicalEVMReceipt(receipt)
	if err != nil {
		return nil, err
	}
	if txEvidence.BlockHash == nil || txEvidence.BlockNumber == nil ||
		txEvidence.Hash != receiptEvidence.TransactionHash || *txEvidence.BlockHash != receiptEvidence.BlockHash ||
		*txEvidence.BlockNumber != receiptEvidence.BlockNumber {
		return nil, ErrTransactionData
	}
	return json.Marshal(evmEvidence{Transaction: txEvidence, Receipt: &receiptEvidence})
}

func canonicalEVMTransaction(tx gjson.Result) (evmTransactionEvidence, error) {
	var out evmTransactionEvidence
	var err error
	if out.Hash, err = evmHex(tx.Get("hash"), 32); err != nil {
		return out, err
	}
	if out.From, err = evmHex(tx.Get("from"), 20); err != nil {
		return out, err
	}
	if out.Input, err = evmHex(tx.Get("input"), -1); err != nil {
		return out, err
	}
	if out.Value, err = evmQuantity(tx.Get("value")); err != nil {
		return out, err
	}
	if out.BlockHash, err = evmHexPtr(tx.Get("blockHash"), 32); err != nil {
		return out, err
	}
	if out.BlockNumber, err = evmQuantityPtr(tx.Get("blockNumber")); err != nil {
		return out, err
	}
	if out.To, err = evmHexPtr(tx.Get("to"), 20); err != nil {
		return out, err
	}
	return out, nil
}

func canonicalEVMReceipt(receipt gjson.Result) (evmReceiptEvidence, error) {
	var out evmReceiptEvidence
	var err error
	if out.TransactionHash, err = evmHex(receipt.Get("transactionHash"), 32); err != nil {
		return out, err
	}
	if out.BlockHash, err = evmHex(receipt.Get("blockHash"), 32); err != nil {
		return out, err
	}
	if out.BlockNumber, err = evmQuantity(receipt.Get("blockNumber")); err != nil {
		return out, err
	}
	if out.Status, err = evmQuantity(receipt.Get("status")); err != nil || (out.Status != "0x0" && out.Status != "0x1") {
		return out, ErrTransactionData
	}
	logs := receipt.Get("logs")
	if !logs.IsArray() {
		return out, ErrTransactionData
	}
	out.Logs = make([]evmLogEvidence, 0, len(logs.Array()))
	for _, raw := range logs.Array() {
		log, err := canonicalEVMLog(raw, out.TransactionHash, out.BlockHash)
		if err != nil {
			return out, err
		}
		out.Logs = append(out.Logs, log)
	}
	return out, nil
}

func canonicalEVMLog(raw gjson.Result, txHash, blockHash string) (evmLogEvidence, error) {
	var out evmLogEvidence
	var err error
	if out.Address, err = evmHex(raw.Get("address"), 20); err != nil {
		return out, err
	}
	topics := raw.Get("topics")
	if !topics.IsArray() {
		return out, ErrTransactionData
	}
	out.Topics = make([]string, 0, len(topics.Array()))
	for _, topic := range topics.Array() {
		value, err := evmHex(topic, 32)
		if err != nil {
			return out, err
		}
		out.Topics = append(out.Topics, value)
	}
	if out.Data, err = evmHex(raw.Get("data"), -1); err != nil {
		return out, err
	}
	// A nested receipt log already identifies its transaction and block. Check
	// duplicate identifiers when supplied and return the verified parent IDs.
	out.TransactionHash, out.BlockHash = txHash, blockHash
	if id := raw.Get("transactionHash"); id.Exists() && !strings.EqualFold(id.String(), txHash) {
		return out, ErrTransactionData
	}
	if id := raw.Get("blockHash"); id.Exists() && !strings.EqualFold(id.String(), blockHash) {
		return out, ErrTransactionData
	}
	switch removed := raw.Get("removed"); removed.Type {
	case gjson.True, gjson.False:
		out.Removed = removed.Bool()
	default:
		if removed.Exists() {
			return out, ErrTransactionData
		}
	}
	return out, nil
}

func evmHex(value gjson.Result, size int) (string, error) {
	if value.Type != gjson.String {
		return "", ErrTransactionData
	}
	out := strings.ToLower(strings.TrimSpace(value.Str))
	if !strings.HasPrefix(out, "0x") || (size >= 0 && len(out) != 2+size*2) {
		return "", ErrTransactionData
	}
	if _, err := hex.DecodeString(out[2:]); err != nil {
		return "", ErrTransactionData
	}
	return out, nil
}

func evmHexPtr(value gjson.Result, size int) (*string, error) {
	if value.Type == gjson.Null {
		return nil, nil
	}
	out, err := evmHex(value, size)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func evmQuantity(value gjson.Result) (string, error) {
	if value.Type != gjson.String {
		return "", ErrTransactionData
	}
	raw := strings.ToLower(strings.TrimSpace(value.String()))
	if !strings.HasPrefix(raw, "0x") || strings.ContainsAny(raw, "+-") {
		return "", ErrTransactionData
	}
	number, ok := new(big.Int).SetString(raw[2:], 16)
	if !ok {
		return "", ErrTransactionData
	}
	return "0x" + number.Text(16), nil
}

func evmQuantityPtr(value gjson.Result) (*string, error) {
	if !value.Exists() || value.Type == gjson.Null {
		return nil, nil
	}
	out, err := evmQuantity(value)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
