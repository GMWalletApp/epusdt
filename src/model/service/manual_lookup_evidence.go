package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/gagliardetto/solana-go"
	"github.com/tidwall/gjson"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

// Rebuild transfer evidence from an independent transaction lookup.
func manualLookupActivity(e scanner.Activity, raw json.RawMessage) (scanner.Activity, error) {
	v := gjson.ParseBytes(raw)
	invalid := errors.New("transaction is unsuccessful or inconsistent")
	switch {
	case scanner.IsEVM(e.Network):
		tx, r := v.Get("transaction"), v.Get("receipt")
		height, err := strconv.ParseInt(r.Get("blockNumber").String(), 0, 64)
		block := r.Get("blockHash").String()
		if err != nil || height != e.Height || block == "" || tx.Get("blockHash").String() != block || r.Get("status").String() != "0x1" || tx.Get("hash").String() != e.TxID || r.Get("transactionHash").String() != e.TxID || !r.Get("logs").IsArray() {
			return e, invalid
		}
		observed := gjson.GetBytes(e.Data, "transaction.blockHash").String()
		if observed == "" {
			observed = gjson.GetBytes(e.Data, "logs.0.blockHash").String()
		}
		if observed == "" {
			observed = gjson.GetBytes(e.Data, "receipt.blockHash").String()
		}
		if observed != "" && observed != block {
			return e, invalid
		}
		d := scanner.Evidence{Transaction: json.RawMessage(tx.Raw), Receipt: json.RawMessage(r.Raw)}
		for _, l := range r.Get("logs").Array() {
			if l.Get("transactionHash").String() != e.TxID || l.Get("blockHash").String() != block {
				return e, invalid
			}
			d.Logs = append(d.Logs, json.RawMessage(l.Raw))
		}
		e.Data, err = json.Marshal(d)
		return e, err
	case e.Network == "tron":
		if status := v.Get("receipt.receipt.result").String(); status != "" && status != "SUCCESS" {
			return e, invalid
		}
		if v.Get("transaction.txID").String() != e.TxID || v.Get("receipt.id").String() != e.TxID || !v.Get("receipt.blockNumber").Exists() || v.Get("receipt.blockNumber").Int() != e.Height || v.Get("receipt.blockTimeStamp").Int() != e.TimeMS {
			return e, invalid
		}
	case e.Network == "aptos":
		if v.Get("hash").String() != e.TxID || !v.Get("success").Bool() || v.Get("version").Int() != e.Height || v.Get("timestamp").Int()/1000 != e.TimeMS {
			return e, invalid
		}
	case e.Network == "sui":
		if v.Get("digest").String() != e.TxID || !v.Get("effects.status.success").Bool() || !v.Get("checkpoint").Exists() || v.Get("checkpoint").Int() != e.Height {
			return e, invalid
		}
	case e.Network == "solana":
		if v.Get("transaction.signatures.0").String() != e.TxID || v.Get("meta.err").Raw != "null" || v.Get("slot").Int() != e.Height || v.Get("blockTime").Int()*1000 != e.TimeMS {
			return e, invalid
		}
	case e.Network == "ton":
		var observed scanner.TONData
		var independent struct {
			Hash           string          `json:"hash"`
			Account        string          `json:"account"`
			LT             json.RawMessage `json:"lt"`
			Now            uint32          `json:"now"`
			MasterSeq      uint32          `json:"mc_block_seqno"`
			TransactionBOC []byte          `json:"transaction_boc"`
			MasterRootHash string          `json:"mc_block_root_hash"`
		}
		if json.Unmarshal(e.Data, &observed) != nil || json.Unmarshal(raw, &independent) != nil || observed.Account == "" {
			return e, invalid
		}
		independentHash, err := base64.StdEncoding.DecodeString(independent.Hash)
		if err != nil || len(independentHash) != 32 {
			independentHash, err = hex.DecodeString(strings.TrimPrefix(independent.Hash, "0x"))
		}
		account, accountErr := scanner.Canonical(e.Network, independent.Account)
		observedAccount, observedErr := scanner.Canonical(e.Network, observed.Account)
		lt, ltErr := strconv.ParseUint(strings.Trim(string(independent.LT), `"`), 10, 64)
		if err != nil || accountErr != nil || observedErr != nil || account != observedAccount ||
			!strings.EqualFold(hex.EncodeToString(independentHash), e.TxID) || ltErr != nil ||
			int64(independent.Now)*1000 != e.TimeMS || int64(independent.MasterSeq) != e.Height {
			return e, invalid
		}
		parsedAccount, err := address.ParseRawAddr(account)
		if err != nil {
			return e, invalid
		}
		boc := observed.BOC
		if len(independent.TransactionBOC) > 0 {
			boc = independent.TransactionBOC
		}
		c, err := cell.FromBOC(boc)
		if err != nil {
			return e, invalid
		}
		var tx tlb.Transaction
		if err = tlb.LoadFromCell(&tx, c.BeginParse()); err != nil ||
			!strings.EqualFold(hex.EncodeToString(c.Hash()), e.TxID) ||
			!strings.EqualFold(hex.EncodeToString(tx.AccountAddr), hex.EncodeToString(parsedAccount.Data())) ||
			tx.LT != lt || tx.Now != independent.Now {
			return e, invalid
		}
		if independent.MasterRootHash != "" && observed.Master != nil {
			rootHash, decodeErr := base64.StdEncoding.DecodeString(independent.MasterRootHash)
			if decodeErr != nil || hex.EncodeToString(rootHash) != hex.EncodeToString(observed.Master.RootHash) {
				return e, invalid
			}
		}
		observed.Account = account
		observed.BOC = boc
		e.Data, err = json.Marshal(observed)
		if err != nil {
			return e, err
		}
		return e, nil
	default:
		return e, fmt.Errorf("unsupported payment network %s", e.Network)
	}
	e.Data = raw
	return e, nil
}

// Solana hash lookups use raw JSON (including address-table lookups). Decode
// the same transfer instructions that the subscription receives as jsonParsed.
func compiledSolanaTransfer(ins, tx gjson.Result) (*TransferInfo, error) {
	keys := append([]gjson.Result{}, tx.Get("transaction.message.accountKeys").Array()...)
	keys = append(keys, tx.Get("meta.loadedAddresses.writable").Array()...)
	keys = append(keys, tx.Get("meta.loadedAddresses.readonly").Array()...)
	key := func(index gjson.Result) string {
		if index.Type != gjson.Number || index.Int() < 0 || index.Int() >= int64(len(keys)) {
			return ""
		}
		return keys[index.Int()].String()
	}
	program := key(ins.Get("programIdIndex"))
	if program != SystemProgramID && program != TokenProgramID && program != Token2022ProgramID {
		return nil, nil
	}
	var encoded solana.Base58
	if err := json.Unmarshal([]byte(ins.Get("data").Raw), &encoded); err != nil {
		return nil, err
	}
	accounts := ins.Get("accounts").Array()
	if program == SystemProgramID && len(encoded) == 12 && binary.LittleEndian.Uint32(encoded) == 2 && len(accounts) >= 2 {
		return &TransferInfo{Source: key(accounts[0]), Destination: key(accounts[1]), Mint: "SOL", RawAmount: binary.LittleEndian.Uint64(encoded[4:])}, nil
	}
	if program == SystemProgramID || len(accounts) < 3 {
		return nil, nil
	}
	var dest int
	var mint string
	var decimals int
	switch {
	case len(encoded) == 10 && encoded[0] == 12 && len(accounts) >= 4:
		dest = 2
		mint = key(accounts[1])
		decimals = int(encoded[9])
	case len(encoded) == 9 && encoded[0] == 3:
		dest = 1
		for _, b := range tx.Get("meta.postTokenBalances").Array() {
			if b.Get("accountIndex").Raw == accounts[dest].Raw {
				mint = b.Get("mint").String()
				decimals = int(b.Get("uiTokenAmount.decimals").Int())
				break
			}
		}
	default:
		return nil, nil
	}
	if mint == "" || key(accounts[0]) == "" || key(accounts[dest]) == "" {
		return nil, nil
	}
	d := decimals
	return &TransferInfo{Source: key(accounts[0]), Destination: key(accounts[dest]), Mint: mint, Decimals: &d, RawAmount: binary.LittleEndian.Uint64(encoded[1:9])}, nil
}

// Match a manually supplied hash when the scanner provides the transfer evidence.
func confirmManualTransfer(ctx context.Context, order *mdb.Orders, hash string, raw json.RawMessage) error {
	dest, err := scanner.Canonical(order.Network, order.ReceiveAddress)
	if err != nil {
		return err
	}
	v := gjson.ParseBytes(raw)
	timeMS := order.CreatedAt.TimestampMilli()
	if timeMS <= 0 {
		timeMS = 1
	}
	e := scanner.Activity{Network: order.Network, TxID: hash, Addresses: []string{dest}, TimeMS: timeMS}
	switch {
	case scanner.IsEVM(order.Network):
		e.Height, err = strconv.ParseInt(v.Get("receipt.blockNumber").String(), 0, 64)
		if err != nil {
			return err
		}
	case order.Network == "tron":
		e.Height = v.Get("receipt.blockNumber").Int()
		e.TimeMS = v.Get("receipt.blockTimeStamp").Int()
	case order.Network == "aptos":
		e.Height = v.Get("version").Int()
		e.TimeMS = v.Get("timestamp").Int() / 1000
	case order.Network == "solana":
		e.Height = v.Get("slot").Int()
		e.TimeMS = v.Get("blockTime").Int() * 1000
	case order.Network == "sui":
		e.Height = v.Get("checkpoint").Int()
	}
	if e.TimeMS < order.CreatedAt.TimestampMilli() {
		return errors.New("transaction predates the order")
	}
	verified, err := manualLookupActivity(e, raw)
	if err != nil {
		return err
	}
	_, err = verifiedOrderCredit(ctx, order, verified)
	return err
}
