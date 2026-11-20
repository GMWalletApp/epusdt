package chains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

func (s *jsonSource) tron(ctx context.Context, h int64) (int64, []Activity, error) {
	return s.tronBlock(ctx, h, "")
}
func (s *jsonSource) tronBlock(ctx context.Context, h int64, hash string) (int64, []Activity, error) {
	params := map[string]int64{"num": h}
	b, err := s.request(ctx, "/wallet/getblockbynum", params)
	if err != nil {
		return 0, nil, err
	}
	if hash != "" && !strings.EqualFold(hash, b.Get("blockID").String()) {
		return 0, nil, errors.New("TRON JSON-RPC and Wallet block hashes differ")
	}
	if b.Get("blockID").String() == "" || b.Get("block_header.raw_data.number").Int() != h {
		return 0, nil, errors.New("missing TRON block")
	}
	txs := b.Get("transactions").Array()
	if len(txs) == 0 {
		return h, nil, nil
	}
	rs, err := s.request(ctx, "/wallet/gettransactioninfobyblocknum", params)
	if err != nil {
		return 0, nil, err
	}
	if !rs.IsArray() {
		return 0, nil, errors.New("missing TRON receipts")
	}
	return tronActivities(h, b.Get("block_header.raw_data.timestamp").Int(), txs, rs)
}
func tronActivities(h, stamp int64, txs []gjson.Result, rs gjson.Result) (int64, []Activity, error) {
	receipts := map[string]gjson.Result{}
	for _, r := range rs.Array() {
		receipts[r.Get("id").String()] = r
	}
	var out []Activity
	for _, tx := range txs {
		id := tx.Get("txID").String()
		r, ok := receipts[id]
		if !ok || r.Get("blockNumber").Int() != h {
			return 0, nil, errors.New("incomplete TRON receipts")
		}
		e := Activity{Height: h, TxID: id, TimeMS: stamp, Data: object(Evidence{Transaction: json.RawMessage(tx.Raw), Receipt: json.RawMessage(r.Raw)})}
		for _, c := range tx.Get("raw_data.contract").Array() {
			var visit func(gjson.Result)
			visit = func(v gjson.Result) {
				v.ForEach(func(k, x gjson.Result) bool {
					if strings.HasSuffix(k.String(), "address") {
						addAddress(&e.Addresses, "tron", x.String())
					}
					if x.IsObject() || x.IsArray() {
						visit(x)
					}
					return true
				})
			}
			visit(c.Get("parameter.value"))
		}
		for _, l := range r.Get("log").Array() {
			addAddress(&e.Addresses, "tron", l.Get("address").String())
			topics := l.Get("topics").Array()
			if len(topics) > 1 {
				indexTopics(&e.Addresses, "tron", topics[1:])
			}
		}
		for _, i := range r.Get("internal_transactions").Array() {
			addAddress(&e.Addresses, "tron", i.Get("caller_address").String())
			addAddress(&e.Addresses, "tron", i.Get("transferTo_address").String())
		}
		addAddress(&e.Addresses, "tron", r.Get("contract_address").String())
		out = append(out, e)
	}
	return h, out, nil
}

func (s *jsonSource) aptos(ctx context.Context, start, head int64) (int64, []Activity, error) {
	rows, err := s.request(ctx, fmt.Sprintf("/v1/transactions?start=%d&limit=%d", start, min(head-start+1, 100)), nil)
	if err != nil {
		return 0, nil, err
	}
	if !rows.IsArray() || len(rows.Array()) == 0 {
		return 0, nil, errors.New("missing Aptos versions")
	}
	end := start - 1
	var out []Activity
	for _, tx := range rows.Array() {
		if !tx.Get("version").Exists() || tx.Get("version").Int() != end+1 || tx.Get("hash").String() == "" {
			return 0, nil, errors.New("non-contiguous Aptos versions")
		}
		end++
		e := Activity{Height: end, TxID: tx.Get("hash").String(), TimeMS: tx.Get("timestamp").Int() / 1000, Data: json.RawMessage(tx.Raw)}
		// Include senders, signers, event accounts, object owners, write-set
		// addresses and address-valued Move arguments. Payment interprets the events.
		var walk func(gjson.Result)
		walk = func(v gjson.Result) {
			if v.IsObject() || v.IsArray() {
				v.ForEach(func(_, x gjson.Result) bool { walk(x); return true })
			} else if v.Type == gjson.String && strings.HasPrefix(v.Str, "0x") {
				a, _, _ := strings.Cut(v.Str, "::")
				addAddress(&e.Addresses, "aptos", a)
			}
		}
		for _, p := range []string{"sender", "signature", "payload", "events", "changes", "fee_payer_address"} {
			walk(tx.Get(p))
		}
		out = append(out, e)
	}
	return end, out, nil
}
