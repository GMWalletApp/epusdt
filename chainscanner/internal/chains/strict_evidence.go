package chains

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
)

// A nil value omits an optional field. RawMessage("null") preserves a protocol null.
type evidenceRule func(gjson.Result) (any, error)

var ErrTransactionPending = errors.New("transaction is pending confirmation")

type evidenceFields map[string]evidenceRule

func (fields evidenceFields) project(a gjson.Result) (any, error) {
	if !a.IsObject() {
		return nil, ErrTransactionData
	}
	out := make(map[string]any, len(fields))
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		v, err := fields[key](a.Get(key))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if v != nil {
			out[key] = v
		}
	}
	return out, nil
}

// Input comes from canonicalTransaction, which sorts object keys and keeps
// integers exact. Move values are defined by on-chain application types.
func rawValue(a gjson.Result) (any, error) {
	if !a.Exists() {
		return nil, ErrTransactionData
	}
	return json.RawMessage(a.Raw), nil
}

func textValue(a gjson.Result) (any, error) {
	if a.Type != gjson.String || a.Str == "" {
		return nil, ErrTransactionData
	}
	return rawValue(a)
}

// A compiled Solana instruction can have empty instruction data.
func stringValue(a gjson.Result) (any, error) {
	if a.Type != gjson.String {
		return nil, ErrTransactionData
	}
	return rawValue(a)
}

func boolValue(a gjson.Result) (any, error) {
	if a.Type != gjson.True && a.Type != gjson.False {
		return nil, ErrTransactionData
	}
	return rawValue(a)
}

func optional(rule evidenceRule) evidenceRule {
	return func(a gjson.Result) (any, error) {
		if a.Type == gjson.Null {
			return nil, nil
		}
		return rule(a)
	}
}

func list(rule evidenceRule) evidenceRule {
	return func(a gjson.Result) (any, error) {
		if !a.IsArray() {
			return nil, ErrTransactionData
		}
		items := a.Array()
		out := make([]any, len(items))
		for i := range items {
			v, err := rule(items[i])
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = v
		}
		return out, nil
	}
}

// Protobuf omits empty repeated fields.
func repeated(rule evidenceRule) evidenceRule {
	items := list(rule)
	return func(v gjson.Result) (any, error) {
		if !v.Exists() {
			return []any{}, nil
		}
		return items(v)
	}
}

func solExecutionError(v gjson.Result) (any, error) {
	if v.Raw != "null" && v.Type != gjson.String && !v.IsObject() {
		return nil, ErrTransactionData
	}
	return rawValue(v)
}

var solInstruction = evidenceFields{
	"programIdIndex": uintValue, "accounts": list(uintValue), "data": stringValue,
}

var solBalance = evidenceFields{
	"accountIndex": uintValue, "mint": textValue, "owner": optional(textValue),
	"uiTokenAmount": evidenceFields{"amount": decimalText, "decimals": uintValue}.project,
}

var solEvidence = evidenceFields{
	"slot": uintValue, "blockTime": optional(uintValue),
	"transaction": evidenceFields{
		"signatures": list(textValue),
		"message": evidenceFields{
			"accountKeys": list(textValue), "instructions": list(solInstruction.project),
			"recentBlockhash": textValue,
		}.project,
	}.project,
	"meta": evidenceFields{
		"err":               solExecutionError,
		"innerInstructions": optional(list(evidenceFields{"index": uintValue, "instructions": list(solInstruction.project)}.project)),
		"loadedAddresses":   optional(evidenceFields{"writable": list(textValue), "readonly": list(textValue)}.project),
		"postTokenBalances": optional(list(solBalance.project)),
	}.project,
}

var aptosEvidence = evidenceFields{
	"hash": textValue, "type": textValue, "version": decimalText, "timestamp": decimalText, "success": boolValue,
	"events":  optional(list(evidenceFields{"type": textValue, "data": rawValue}.project)),
	"changes": list(aptosChange),
}

func aptosChange(a gjson.Result) (any, error) {
	fields := evidenceFields{"type": textValue, "state_key_hash": textValue}
	switch a.Get("type").Str {
	case "write_resource":
		fields["address"] = textValue
		fields["data"] = evidenceFields{"type": textValue, "data": rawValue}.project
	case "write_table_item":
		fields["handle"], fields["key"], fields["value"] = textValue, textValue, textValue
	}
	return fields.project(a)
}

func digits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

func uintValue(a gjson.Result) (any, error) {
	if a.Type != gjson.Number || !digits(a.Raw) {
		return nil, ErrTransactionData
	}
	return rawValue(a)
}

func decimalText(a gjson.Result) (any, error) {
	if !digits(a.Str) {
		return nil, ErrTransactionData
	}
	return textValue(a)
}

var suiEvidence = evidenceFields{
	"digest": textValue, "checkpoint": decimalText, "timestamp": optional(textValue),
	"transaction":     evidenceFields{"sender": textValue}.project,
	"effects":         evidenceFields{"status": evidenceFields{"success": boolValue}.project}.project,
	"balance_changes": repeated(evidenceFields{"address": textValue, "coin_type": textValue, "amount": signedDecimal}.project),
}

func signedDecimal(a gjson.Result) (any, error) {
	if !digits(strings.TrimPrefix(a.Str, "-")) {
		return nil, ErrTransactionData
	}
	return textValue(a)
}

func tronHexBytes(a gjson.Result) (any, error) {
	if a.Type != gjson.String {
		return nil, ErrTransactionData
	}
	if _, err := hex.DecodeString(a.Str); err != nil {
		return nil, ErrTransactionData
	}
	return strings.ToLower(a.Str), nil
}

var tronEvidence = evidenceFields{
	"transaction": evidenceFields{
		"txID": tronHexBytes, "raw_data_hex": optional(tronHexBytes), "ret": list(evidenceFields{"contractRet": textValue}.project),
		"raw_data": evidenceFields{
			"contract": list(tronContract),
		}.project,
	}.project,
	"receipt": evidenceFields{
		"id": tronHexBytes, "blockNumber": uintValue, "blockTimeStamp": uintValue, "result": optional(textValue),
		"receipt": tronReceipt,
		"log":     repeated(evidenceFields{"address": tronHexBytes, "topics": repeated(tronHexBytes), "data": tronLogData}.project),
	}.project,
}

func tronContract(a gjson.Result) (any, error) {
	fields := evidenceFields{"type": textValue}
	var value evidenceFields
	switch a.Get("type").Str {
	case "TransferContract":
		value = evidenceFields{"owner_address": tronHexBytes, "to_address": tronHexBytes, "amount": uintValue}
	case "TriggerSmartContract":
		value = evidenceFields{"owner_address": tronHexBytes, "contract_address": tronHexBytes, "data": tronLogData,
			"call_value": optional(uintValue), "call_token_value": optional(uintValue), "token_id": optional(uintValue)}
	}
	if value != nil {
		fields["parameter"] = evidenceFields{"value": value.project}.project
	}
	return fields.project(a)
}

func tronLogData(a gjson.Result) (any, error) {
	if !a.Exists() {
		return "", nil
	}
	return tronHexBytes(a)
}

func tronReceipt(a gjson.Result) (any, error) {
	if a.Exists() && !a.IsObject() {
		return nil, ErrTransactionData
	}
	v, err := optional(textValue)(a.Get("result"))
	if err != nil || v == nil {
		return nil, err
	}
	return map[string]any{"result": v}, nil
}

// Use raw message contents, not the indexer's optional ABI decoding.
var tonMessage = evidenceFields{
	"hash": tonHash, "source": optional(tonAccount), "destination": optional(tonAccount),
	"value": optional(decimalText), "bounced": optional(boolValue), "bounce": optional(boolValue),
	"message_content": optional(evidenceFields{"hash": tonHash, "body": textValue}.project),
}

func nullableMessage(a gjson.Result) (any, error) {
	if !a.Exists() {
		return nil, nil
	}
	if a.Type == gjson.Null {
		return rawValue(a)
	}
	return tonMessage.project(a)
}

var tonIndexEvidence = evidenceFields{
	"hash": tonHash, "account": tonAccount, "lt": decimalText, "now": uintValue, "mc_block_seqno": uintValue,
	"block_ref":   evidenceFields{"workchain": rawValue, "shard": textValue, "seqno": uintValue}.project,
	"description": optional(evidenceFields{"type": textValue, "aborted": optional(boolValue), "destroyed": optional(boolValue)}.project),
	"in_msg":      nullableMessage, "out_msgs": optional(list(tonMessage.project)),
}

func tonHash(a gjson.Result) (any, error) {
	left, err := TransactionHash("ton", a.Str)
	if err != nil {
		return nil, err
	}
	raw, _ := hex.DecodeString(left)
	return base64.StdEncoding.EncodeToString(raw), nil
}

func tonAccount(a gjson.Result) (any, error) {
	return Canonical("ton", a.Str)
}

func checkStrictRecord(n Network, v gjson.Result) error {
	switch {
	case IsEVM(n.Name):
		if v.Get("receipt").Raw == "null" {
			return ErrTransactionPending
		}
	case n.Name == "aptos":
		switch v.Get("type").Str {
		case "pending_transaction":
			return ErrTransactionPending
		case "user_transaction":
			if !v.Get("events").IsArray() {
				return ErrTransactionData
			}
		}
	case n.Name == "tron":
		if r := v.Get("receipt"); r.IsObject() && len(r.Map()) == 0 {
			return ErrTransactionPending
		}
		count := len(v.Get("transaction.raw_data.contract").Array())
		if count == 0 || len(v.Get("transaction.ret").Array()) != count {
			return ErrTransactionData
		}
	case n.Name == "ton" && n.Protocol == "toncenter-v3":
		if flag := v.Get("emulated"); flag.Exists() && flag.Type != gjson.False {
			return ErrTransactionData
		}
		if finality := v.Get("finality"); finality.Exists() && finality.Str != "finalized" {
			return ErrTransactionPending
		}
	}
	return nil
}

// TransactionEvidence validates and projects a single successful,
// identity-checked read from one node.
func TransactionEvidence(n Network, raw json.RawMessage) (json.RawMessage, error) {
	v := gjson.ParseBytes(raw)
	if !v.IsObject() {
		return nil, ErrTransactionData
	}
	if err := checkStrictRecord(n, v); err != nil {
		return nil, err
	}
	if IsEVM(n.Name) {
		return canonicalEVMEvidence(v.Get("transaction"), v.Get("receipt"))
	}
	if n.Name == "bitcoin" {
		return canonicalBitcoinEvidence(v)
	}
	if n.Name == "ton" && n.Protocol == "lite" {
		return raw, nil
	}

	var fields evidenceFields
	switch {
	case n.Name == "solana":
		fields = solEvidence
	case n.Name == "aptos":
		fields = aptosEvidence
	case n.Name == "sui":
		fields = suiEvidence
	case n.Name == "tron":
		fields = tronEvidence
	case n.Name == "ton" && n.Protocol == "toncenter-v3":
		fields = tonIndexEvidence
	default:
		return nil, ErrTransactionData
	}
	projected, err := fields.project(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(projected)
}
