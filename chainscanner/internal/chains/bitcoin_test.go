package chains

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/testutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/tidwall/gjson"
)

func TestBitcoinScanSpendChangeAndIncompletePrevout(t *testing.T) {
	type obj = map[string]any
	hash := strings.Repeat("b", 64)
	id := strings.Repeat("a", 64)
	script := obj{"hex": "76a914" + strings.Repeat("00", 20) + "88ac"}
	recipient := obj{"hex": "0014" + strings.Repeat("11", 20)}
	prev := obj{"txid": id, "vout": 0, "prevout": obj{"scriptPubKey": script}}
	spend := obj{"txid": id, "vin": []obj{prev}, "vout": []obj{{"scriptPubKey": recipient}, {"scriptPubKey": script}}}
	coinbase := obj{"txid": strings.Repeat("1", 64), "vin": []obj{{"coinbase": "00"}}, "vout": []obj{{"scriptPubKey": obj{"hex": "6a"}}}}
	block := obj{"height": 1, "hash": hash, "previousblockhash": bitcoinGenesis, "time": 12345, "nTx": 2, "tx": []obj{coinbase, spend}}
	srv := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "getblockhash":
			if string(r.Params[0]) == "0" {
				return bitcoinGenesis, nil
			}
			return hash, nil
		case "getblock":
			if string(r.Params[1]) != "3" {
				t.Error("missing prevouts")
			}
			return block, nil
		}
		return nil, errors.New("unexpected method")
	})
	defer srv.Close()
	s, e := NewSource(t.Context(), Network{Name: "bitcoin", URL: srv.URL})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_, link, rows, e := ReadSpan(t.Context(), "bitcoin", s, Span{First: 1, Last: 1})
	if e != nil || link.Parent != bitcoinGenesis || link.Hash != hash || len(rows) != 2 || len(rows[0].Addresses) != 0 || len(rows[1].Addresses) != 2 || !slices.Contains(rows[1].Addresses, "1111111111111111111114oLvT2") {
		t.Fatal(link, rows, e)
	}
	delete(prev, "prevout")
	if _, _, e := s.Read(t.Context(), 1, 1); e == nil {
		t.Fatal("incomplete spent output advanced scan")
	}
}

func TestBitcoinAddressNetworkAndCase(t *testing.T) {
	const a = "bc1qzyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3h8ffkz"
	if v, e := Canonical("bitcoin", strings.ToUpper(a)); e != nil || v != a {
		t.Fatal(v, e)
	}
	for _, bad := range []string{"mipcBbFg9gMiCh81Kj8tqqdgoZub1ZJRfn", "bc1Q" + a[4:], "1111111111111111111114oLvT3"} {
		if _, e := Canonical("bitcoin", bad); e == nil {
			t.Fatal(bad)
		}
	}
}

func sampleBitcoinTx(t *testing.T) (txid, rawHex string) {
	t.Helper()
	msg := wire.NewMsgTx(wire.TxVersion)
	prevHash, err := chainhash.NewHashFromStr(strings.Repeat("11", 32))
	if err != nil {
		t.Fatal(err)
	}
	msg.AddTxIn(wire.NewTxIn(wire.NewOutPoint(prevHash, 0), nil, nil))
	pkScript, err := hex.DecodeString("0014" + strings.Repeat("22", 20))
	if err != nil {
		t.Fatal(err)
	}
	msg.AddTxOut(wire.NewTxOut(100000, pkScript))
	var buf bytes.Buffer
	if err := msg.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	return msg.TxHash().String(), hex.EncodeToString(buf.Bytes())
}

func TestBitcoinTransactionEvidenceAndLookup(t *testing.T) {
	txid, rawHex := sampleBitcoinTx(t)
	blockHash := strings.Repeat("c", 64)
	n := Network{Name: "bitcoin", Protocol: "jsonrpc"}

	confirmed := object(map[string]any{
		"txid":       strings.ToUpper(txid),
		"hex":        strings.ToUpper(rawHex),
		"blockhash":  strings.ToUpper(blockHash),
		"diagnostic": "provider-only",
	})
	got, err := TransactionEvidence(n, confirmed)
	if err != nil {
		t.Fatalf("confirmed evidence rejected: %v", err)
	}
	if gjson.GetBytes(got, "txid").Str != txid || gjson.GetBytes(got, "hex").Str != rawHex || gjson.GetBytes(got, "blockhash").Str != blockHash || gjson.GetBytes(got, "diagnostic").Exists() {
		t.Fatalf("unexpected canonical bitcoin evidence: %s", got)
	}

	pending := object(map[string]any{"txid": txid, "hex": rawHex, "blockhash": ""})
	if res, err := TransactionEvidence(n, pending); err != ErrTransactionPending || res != nil {
		t.Fatalf("unconfirmed bitcoin tx got (%s, %v), want (nil, ErrTransactionPending)", res, err)
	}

	for _, bad := range []json.RawMessage{
		json.RawMessage(`{}`),
		object(map[string]any{"txid": strings.Repeat("0", 64), "hex": rawHex, "blockhash": blockHash}),
		object(map[string]any{"txid": txid, "hex": "deadbeef", "blockhash": blockHash}),
		object(map[string]any{"txid": txid, "hex": rawHex, "blockhash": "not-a-hash"}),
		object(map[string]any{"txid": strings.Repeat("0", 64), "hex": rawHex, "blockhash": ""}),
	} {
		if _, err := TransactionEvidence(n, bad); !errors.Is(err, ErrTransactionData) {
			t.Fatalf("bad bitcoin evidence %s: got err=%v, want ErrTransactionData", bad, err)
		}
	}

	var reply any
	srv := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "getblockhash":
			return bitcoinGenesis, nil
		case "getrawtransaction":
			return reply, nil
		default:
			return nil, errors.New("unexpected method")
		}
	})
	defer srv.Close()
	net := Network{Name: "bitcoin", Protocol: "jsonrpc", URL: srv.URL}

	reply = map[string]any{"txid": txid, "hex": rawHex, "blockhash": blockHash, "confirmations": 2, "vin": []any{map[string]any{}}, "vout": []any{map[string]any{}}}
	raw, err := LookupTransaction(t.Context(), net, txid, false)
	if err != nil {
		t.Fatalf("confirmed lookup failed: %v", err)
	}
	if _, err := TransactionEvidence(net, raw); err != nil {
		t.Fatalf("lookup result failed evidence validation: %v", err)
	}

	reply = map[string]any{"txid": txid, "hex": rawHex, "blockhash": "", "confirmations": 0, "vin": []any{map[string]any{}}, "vout": []any{map[string]any{}}}
	raw, err = LookupTransaction(t.Context(), net, txid, false)
	if err != nil {
		t.Fatalf("unconfirmed lookup failed: %v", err)
	}
	if _, err := TransactionEvidence(net, raw); err != ErrTransactionPending {
		t.Fatalf("unconfirmed lookup evidence got %v, want ErrTransactionPending", err)
	}
}
