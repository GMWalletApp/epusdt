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
	Removed         bool     `json:"removed"`
}

func canonicalEVMEvidence(tx, receipt gjson.Result) (json.RawMessage, error) {
	txEvidence, err := canonicalEVMTransaction(tx)
	if err != nil {
		return nil, err
	}
	if !receipt.Exists() || receipt.Raw == "null" {
		return json.Marshal(evmEvidence{Transaction: txEvidence})
	}
	receiptEvidence, err := canonicalEVMReceipt(receipt)
	if err != nil {
		return nil, err
	}
	return json.Marshal(evmEvidence{Transaction: txEvidence, Receipt: &receiptEvidence})
}

func canonicalEVMTransaction(tx gjson.Result) (evmTransactionEvidence, error) {
	var out evmTransactionEvidence
	var err error
	if out.Hash, err = evmHex(tx.Get("hash"), true); err != nil {
		return out, err
	}
	if out.From, err = evmHex(tx.Get("from"), true); err != nil {
		return out, err
	}
	if out.Input, err = evmHex(tx.Get("input"), true); err != nil {
		return out, err
	}
	if out.Value, err = evmQuantity(tx.Get("value"), false); err != nil {
		return out, err
	}
	if out.BlockHash, err = evmHexPtr(tx.Get("blockHash")); err != nil {
		return out, err
	}
	if out.BlockNumber, err = evmQuantityPtr(tx.Get("blockNumber")); err != nil {
		return out, err
	}
	if out.To, err = evmHexPtr(tx.Get("to")); err != nil {
		return out, err
	}
	return out, nil
}

func canonicalEVMReceipt(receipt gjson.Result) (evmReceiptEvidence, error) {
	var out evmReceiptEvidence
	var err error
	if out.TransactionHash, err = evmHex(receipt.Get("transactionHash"), true); err != nil {
		return out, err
	}
	if out.BlockHash, err = evmHex(receipt.Get("blockHash"), true); err != nil {
		return out, err
	}
	if out.BlockNumber, err = evmQuantity(receipt.Get("blockNumber"), true); err != nil {
		return out, err
	}
	if out.Status, err = evmQuantity(receipt.Get("status"), true); err != nil {
		return out, err
	}
	logs := receipt.Get("logs")
	if !logs.IsArray() {
		return out, errTxData
	}
	out.Logs = make([]evmLogEvidence, 0, len(logs.Array()))
	for _, raw := range logs.Array() {
		log, err := canonicalEVMLog(raw)
		if err != nil {
			return out, err
		}
		out.Logs = append(out.Logs, log)
	}
	return out, nil
}

func canonicalEVMLog(raw gjson.Result) (evmLogEvidence, error) {
	var out evmLogEvidence
	var err error
	if out.Address, err = evmHex(raw.Get("address"), true); err != nil {
		return out, err
	}
	topics := raw.Get("topics")
	if !topics.IsArray() {
		return out, errTxData
	}
	out.Topics = make([]string, 0, len(topics.Array()))
	for _, topic := range topics.Array() {
		value, err := evmHex(topic, true)
		if err != nil {
			return out, err
		}
		out.Topics = append(out.Topics, value)
	}
	if out.Data, err = evmHex(raw.Get("data"), true); err != nil {
		return out, err
	}
	if out.TransactionHash, err = evmHex(raw.Get("transactionHash"), true); err != nil {
		return out, err
	}
	if out.BlockHash, err = evmHex(raw.Get("blockHash"), true); err != nil {
		return out, err
	}
	out.Removed = raw.Get("removed").Bool()
	return out, nil
}

func evmHex(value gjson.Result, required bool) (string, error) {
	if !value.Exists() || value.Type == gjson.Null {
		if required {
			return "", errTxData
		}
		return "", nil
	}
	out := strings.ToLower(strings.TrimSpace(value.String()))
	if !strings.HasPrefix(out, "0x") {
		return "", errTxData
	}
	if _, err := hex.DecodeString(out[2:]); err != nil {
		return "", errTxData
	}
	return out, nil
}

func evmHexPtr(value gjson.Result) (*string, error) {
	out, err := evmHex(value, false)
	if err != nil || out == "" {
		return nil, err
	}
	return &out, nil
}

func evmQuantity(value gjson.Result, required bool) (string, error) {
	if !value.Exists() || value.Type == gjson.Null {
		if required {
			return "", errTxData
		}
		return "0x0", nil
	}
	raw := strings.ToLower(strings.TrimSpace(value.String()))
	if !strings.HasPrefix(raw, "0x") {
		return "", errTxData
	}
	number, ok := new(big.Int).SetString(raw[2:], 16)
	if !ok {
		return "", errTxData
	}
	return "0x" + number.Text(16), nil
}

func evmQuantityPtr(value gjson.Result) (*string, error) {
	if !value.Exists() || value.Type == gjson.Null {
		return nil, nil
	}
	out, err := evmQuantity(value, true)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
