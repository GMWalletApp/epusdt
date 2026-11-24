package chains

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/tidwall/gjson"
)

func bitcoinAddress(raw string) (string, error) {
	a, err := btcutil.DecodeAddress(raw, &chaincfg.MainNetParams)
	if err == nil && a.IsForNet(&chaincfg.MainNetParams) {
		switch a.(type) {
		case *btcutil.AddressPubKeyHash, *btcutil.AddressScriptHash, *btcutil.AddressWitnessPubKeyHash, *btcutil.AddressWitnessScriptHash, *btcutil.AddressTaproot:
			return a.EncodeAddress(), nil
		}
	}
	return "", errors.New("invalid Bitcoin mainnet address")
}

const bitcoinGenesis = "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f"

func bitcoinHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

// Decode the locking script itself so providers may omit the display address.
// Bare pubkeys and bare multisig do not pay an address-based output script.
func bitcoinScriptAddress(v gjson.Result) (string, error) {
	field := v.Get("hex")
	if field.Type != gjson.String {
		return "", ErrTransactionData
	}
	b, err := hex.DecodeString(field.Str)
	if err != nil {
		return "", ErrTransactionData
	}
	class, addrs, _, err := txscript.ExtractPkScriptAddrs(b, &chaincfg.MainNetParams)
	if err != nil {
		return "", err
	}
	switch class {
	case txscript.PubKeyHashTy, txscript.ScriptHashTy, txscript.WitnessV0PubKeyHashTy, txscript.WitnessV0ScriptHashTy, txscript.WitnessV1TaprootTy:
		if len(addrs) != 1 {
			return "", ErrTransactionData
		}
		return addrs[0].EncodeAddress(), nil
	}
	return "", nil
}

func bitcoinActivity(tx gjson.Result, h, stamp int64, block string, first bool) (Activity, error) {
	id := tx.Get("txid").Str
	inputs, outputs := tx.Get("vin").Array(), tx.Get("vout").Array()
	if !bitcoinHash(id) || len(inputs) == 0 || len(outputs) == 0 {
		return Activity{}, ErrTransactionData
	}
	addresses := []string{}
	seen := map[string]bool{}
	add := func(script gjson.Result) error {
		addr, err := bitcoinScriptAddress(script)
		if addr != "" && !seen[addr] {
			seen[addr] = true
			addresses = append(addresses, addr)
		}
		return err
	}
	for _, input := range inputs {
		if first {
			if len(inputs) != 1 || input.Get("coinbase").Type != gjson.String {
				return Activity{}, ErrTransactionData
			}
			continue
		}
		if !bitcoinHash(input.Get("txid").Str) || input.Get("vout").Type != gjson.Number {
			return Activity{}, ErrTransactionData
		}
		if err := add(input.Get("prevout.scriptPubKey")); err != nil {
			return Activity{}, err
		}
	}
	for _, out := range outputs {
		if err := add(out.Get("scriptPubKey")); err != nil {
			return Activity{}, err
		}
	}
	return Activity{Network: "bitcoin", TxID: id, Height: h, TimeMS: stamp * 1000, Addresses: addresses, Data: object(map[string]any{"block_hash": block, "transaction": json.RawMessage(tx.Raw)})}, nil
}

func (s *jsonSource) bitcoinBlock(ctx context.Context, h int64, link *BlockLink) (int64, []Activity, error) {
	hash, err := s.call(ctx, "getblockhash", h)
	if err != nil {
		return 0, nil, err
	}
	if !bitcoinHash(hash.Str) {
		return 0, nil, ErrTransactionData
	}
	b, err := s.call(ctx, "getblock", hash.Str, 3)
	if err != nil {
		return 0, nil, err
	}
	txs := b.Get("tx").Array()
	parent := b.Get("previousblockhash").Str
	stamp := b.Get("time").Int()
	if b.Get("hash").Str != hash.Str || b.Get("height").Type != gjson.Number || b.Get("height").Int() != h || (h > 0 && !bitcoinHash(parent)) || stamp <= 0 || len(txs) == 0 || b.Get("nTx").Int() != int64(len(txs)) {
		return 0, nil, ErrTransactionData
	}
	rows := make([]Activity, 0, len(txs))
	for i, t := range txs {
		a, e := bitcoinActivity(t, h, stamp, hash.Str, i == 0)
		if e != nil {
			return 0, nil, e
		}
		rows = append(rows, a)
	}
	current, err := s.call(ctx, "getblockhash", h)
	if err != nil {
		return 0, nil, err
	}
	if current.Str != hash.Str {
		return 0, nil, ErrFork
	}
	if link != nil {
		*link = BlockLink{Hash: hash.Str, Parent: parent}
	}
	return h, rows, nil
}

func (s *jsonSource) lookupBitcoin(ctx context.Context, id string) (json.RawMessage, error) {
	tx, err := s.call(ctx, "getrawtransaction", id, true)
	if err != nil {
		var re rpc.Error
		if errors.As(err, &re) && re.ErrorCode() == -5 {
			// An unindexed node cannot report that a historical transaction is absent.
			idx, e := s.call(ctx, "getindexinfo", "txindex")
			if e == nil && idx.Get("txindex.synced").Raw == "true" {
				return nil, ErrTransactionMissing
			}
		}
		return nil, err
	}
	if !strings.EqualFold(tx.Get("txid").Str, id) || len(tx.Get("vin").Array()) == 0 || len(tx.Get("vout").Array()) == 0 {
		return nil, ErrTransactionData
	}
	raw := tx.Get("hex").Str
	b, e := hex.DecodeString(raw)
	reader := bytes.NewReader(b)
	var decoded wire.MsgTx
	if e != nil || decoded.Deserialize(reader) != nil || reader.Len() != 0 || decoded.TxHash().String() != id {
		return nil, ErrTransactionData
	}
	block := tx.Get("blockhash").Str
	if block != "" && (!bitcoinHash(block) || tx.Get("confirmations").Int() < 1) {
		return nil, ErrTransactionData
	}
	return canonicalTransaction(object(map[string]any{"txid": id, "hex": raw, "blockhash": block}))
}

func canonicalBitcoinEvidence(v gjson.Result) (json.RawMessage, error) {
	txidField := v.Get("txid")
	hexField := v.Get("hex")
	blockField := v.Get("blockhash")
	if txidField.Type != gjson.String || hexField.Type != gjson.String {
		return nil, ErrTransactionData
	}
	txid := strings.ToLower(txidField.Str)
	if !bitcoinHash(txid) {
		return nil, ErrTransactionData
	}
	rawHex := strings.ToLower(hexField.Str)
	b, err := hex.DecodeString(rawHex)
	reader := bytes.NewReader(b)
	var decoded wire.MsgTx
	if err != nil || decoded.Deserialize(reader) != nil || reader.Len() != 0 || decoded.TxHash().String() != txid {
		return nil, ErrTransactionData
	}
	if blockField.Type == gjson.String && blockField.Str == "" {
		return nil, ErrTransactionPending
	}
	if blockField.Type != gjson.String {
		return nil, ErrTransactionData
	}
	block := strings.ToLower(blockField.Str)
	if !bitcoinHash(block) {
		return nil, ErrTransactionData
	}
	return json.Marshal(struct {
		BlockHash string `json:"blockhash"`
		Hex       string `json:"hex"`
		TxID      string `json:"txid"`
	}{BlockHash: block, Hex: rawHex, TxID: txid})
}
