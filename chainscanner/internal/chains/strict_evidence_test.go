package chains

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func rpcFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/strict/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	data, err = canonicalTransaction(data)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type omit struct{}

func modifyEvidence(t *testing.T, raw json.RawMessage, path string, value any) json.RawMessage {
	t.Helper()
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	var parent any = root
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		switch p := parent.(type) {
		case map[string]any:
			parent = p[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil {
				t.Fatal(err)
			}
			parent = p[i]
		default:
			t.Fatalf("invalid fixture path %q", path)
		}
	}
	obj := parent.(map[string]any)
	key := parts[len(parts)-1]
	if _, drop := value.(omit); drop {
		delete(obj, key)
	} else {
		obj[key] = value
	}
	result, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStrictRulesOnRPCResponses(t *testing.T) {
	for _, tc := range []struct {
		network, fixture, path string
		change                 any
		agree                  bool
		keep, drop             string
	}{
		{"solana", "solana", "transaction.message.instructions.0.stackHeight", omit{}, true, "meta.err", "transaction.message.instructions.0.stackHeight"},
		{"solana", "solana", "blockTime", nil, true, "slot", "blockTime"},
		{"solana", "solana", "meta.postTokenBalances", omit{}, true, "meta.err", "meta.postTokenBalances"},
		{"solana", "solana", "meta.err", omit{}, false, "", ""},
		{"solana", "solana", "meta.err", false, false, "", ""},
		{"solana", "solana", "transaction.message.instructions.0.data", "changed", true, "transaction.message.instructions.0.data", ""},
		{"aptos", "aptos-epilogue", "vm_status", omit{}, true, "success", "events"},
		{"aptos", "aptos", "vm_status", omit{}, true, "events.0.data.amount", "vm_status"},
		{"aptos", "aptos", "success", false, true, "success", ""},
		{"aptos", "aptos", "events.0.data.amount", "6001", true, "events.0.data.amount", ""},
		{"aptos", "aptos", "changes.0.address", "0x123", true, "changes.0.address", ""},
		{"sui", "sui", "effects.status.error", omit{}, true, "effects.status.success", "effects.status.error"},
		{"sui", "sui", "timestamp", omit{}, true, "balance_changes.0.amount", "timestamp"},
		{"sui", "sui", "balance_changes.0.amount", "12345678901234567890", true, "balance_changes.0.amount", ""},
		{"sui", "sui", "effects.status.success", omit{}, false, "", ""},
		{"tron", "tron", "receipt.receipt.energy_usage", omit{}, true, "receipt.log.0.data", "receipt.receipt.energy_usage"},
		{"tron", "tron-native", "receipt.receipt", omit{}, true, "transaction.ret.0.contractRet", "receipt.receipt"},
		{"tron", "tron-native", "receipt.result", "FAILED", true, "receipt.result", ""},
		{"tron", "tron-native", "receipt.receipt.result", "REVERT", true, "receipt.receipt.result", ""},
		{"tron", "tron", "transaction.raw_data.contract.0.parameter.value.rpc_extension", "extra", true, "transaction.raw_data.contract.0.parameter.value.data", "transaction.raw_data.contract.0.parameter.value.rpc_extension"},
		{"tron", "tron", "receipt.log.0.address", "other", false, "", ""},
		{"tron", "tron", "receipt.log.0.data", omit{}, true, "", ""},
		{"ton", "ton", "in_msg.message_content.decoded", omit{}, true, "in_msg.value", "in_msg.message_content.decoded"},
		{"ton", "ton", "finality", omit{}, true, "mc_block_seqno", "finality"},
		{"ton", "ton", "emulated", true, false, "", ""},
		{"ton", "ton", "in_msg.value", "123456", true, "in_msg.value", ""},
		{"ton", "ton", "mc_block_seqno", 1, true, "mc_block_seqno", ""},
	} {
		t.Run(tc.fixture+"/"+tc.path, func(t *testing.T) {
			original := rpcFixture(t, tc.fixture)
			other := modifyEvidence(t, original, tc.path, tc.change)
			n := Network{Name: tc.network, Protocol: "toncenter-v3"}
			out, err := TransactionEvidence(n, other)
			if (err == nil) != tc.agree {
				t.Fatalf("valid=%v err=%v", tc.agree, err)
			}
			if tc.keep != "" && !gjson.GetBytes(out, tc.keep).Exists() || tc.drop != "" && gjson.GetBytes(out, tc.drop).Exists() {
				t.Fatal(string(out))
			}
		})
	}
}

func TestEVMRPCReceiptsAndCorrelation(t *testing.T) {
	orig := rpcFixture(t, "evm")
	tx := gjson.GetBytes(orig, "transaction")
	receipt := gjson.GetBytes(orig, "receipt")
	want, err := canonicalEVMEvidence(tx, receipt)
	if err != nil {
		t.Fatal(err)
	}
	other := gjson.ParseBytes(rpcFixture(t, "evm-second-receipt"))
	out, err := canonicalEVMEvidence(tx, other)
	if err != nil || !bytes.Equal(out, want) {
		t.Fatalf("independent RPC receipts differ: %v", err)
	}
	for _, tc := range []struct {
		path   string
		change any
		agree  bool
	}{
		{"receipt.blockHash", strings.ToUpper(receipt.Get("blockHash").Str), true},
		{"receipt.blockNumber", "0x00" + receipt.Get("blockNumber").Str[2:], true},
		{"receipt.logs.0.blockHash", omit{}, true},
		{"receipt.logs.0.transactionHash", omit{}, true},
		{"receipt.logs.0.blockHash", tx.Get("hash").Str, false},
		{"receipt.logs.0.removed", "false", false},
		{"transaction.value", omit{}, false},
		{"transaction.value", "0x-1", false},
		{"receipt.status", "0x2", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			changed := modifyEvidence(t, orig, tc.path, tc.change)
			got, err := canonicalEVMEvidence(gjson.GetBytes(changed, "transaction"), gjson.GetBytes(changed, "receipt"))
			if (err == nil) != tc.agree || tc.agree && !bytes.Equal(got, want) {
				t.Fatalf("agreement=%v error=%v", tc.agree, err)
			}
		})
	}
}

func TestStrictPendingHasNoConfirmedResult(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"ethereum", `{"transaction":{},"receipt":null}`},
		{"tron", `{"transaction":{},"receipt":{}}`},
		{"aptos", `{"type":"pending_transaction"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := TransactionEvidence(Network{Name: tc.name}, json.RawMessage(tc.raw))
			if err != ErrTransactionPending || result != nil {
				t.Fatal(string(result), err)
			}
		})
	}
}

func TestFallbackProjectsChainEvidence(t *testing.T) {
	for _, name := range []string{"solana", "tron", "sui", "aptos", "ton"} {
		t.Run(name, func(t *testing.T) {
			n := Network{Name: name}
			if name == "ton" {
				n.Protocol = "toncenter-v3"
			}
			input := rpcFixture(t, name)
			withExtension := modifyEvidence(t, input, "diagnostic", "one provider only")
			got, err := TransactionEvidence(n, withExtension)
			if err != nil {
				t.Fatal(err)
			}
			want, err := TransactionEvidence(n, input)
			if err != nil || !bytes.Equal(got, want) || gjson.GetBytes(got, "diagnostic").Exists() {
				t.Fatalf("inconsistent evidence: %s, %v", got, err)
			}
		})
	}
}
