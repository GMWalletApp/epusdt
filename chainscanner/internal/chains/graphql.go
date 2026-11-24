package chains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/tidwall/gjson"
)

type graphSource struct{ *jsonSource }

func newGraph(ctx context.Context, n Network) (Source, error) {
	s := &graphSource{&jsonSource{n: n, http: newNodeHTTPClient(30 * time.Second)}}
	if _, err := s.Head(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *graphSource) query(ctx context.Context, q string, vars map[string]any) (gjson.Result, error) {
	r, e := s.request(ctx, "", map[string]any{"query": q, "variables": vars})
	if e != nil {
		return r, e
	}
	if len(r.Get("errors").Array()) != 0 || !r.Get("data").IsObject() {
		return gjson.Result{}, errors.New("incomplete GraphQL response")
	}
	return r.Get("data"), nil
}
func (s *graphSource) Head(ctx context.Context) (int64, error) {
	if s.n.Name == "sui" {
		r, e := s.query(ctx, "{chainIdentifier checkpoint {sequenceNumber}}", nil)
		if e != nil {
			return 0, e
		}
		if r.Get("chainIdentifier").String() != "4btiuiMPvEENsttpZC7CZ53DruC3MAgfznDbASZ7DR6S" {
			return 0, errors.New("expected Sui mainnet")
		}
		return strconv.ParseInt(r.Get("checkpoint.sequenceNumber").String(), 10, 64)
	}
	r, e := s.query(ctx, "{chainID block {number}}", nil)
	if e != nil {
		return 0, e
	}
	id, e := strconv.ParseInt(r.Get("chainID").String(), 0, 64)
	if e != nil || id != EVMChainID(s.n.Name) {
		return 0, errors.New("unexpected execution chain ID")
	}
	return strconv.ParseInt(r.Get("block.number").String(), 0, 64)
}
func (s *graphSource) Read(ctx context.Context, h, head int64) (int64, []Activity, error) {
	if s.n.Name == "sui" {
		return s.suiCheckpoint(ctx, h)
	}
	return s.ReadLinkedBlock(ctx, h, nil)
}
func (s *graphSource) ReadLinkedBlock(ctx context.Context, h int64, link *BlockLink) (int64, []Activity, error) {
	const q = `query($height:Long!){block(number:$height){number hash parent{hash} timestamp transactionCount transactions{hash from{address} to{address} value inputData status index createdContract{address} logs{index account{address} topics data}}}}`
	r, e := s.query(ctx, q, map[string]any{"height": fmt.Sprintf("0x%x", h)})
	if e != nil {
		return 0, nil, e
	}
	b := r.Get("block")
	number, e1 := strconv.ParseInt(b.Get("number").String(), 0, 64)
	stamp, e2 := strconv.ParseInt(b.Get("timestamp").String(), 0, 64)
	count, e3 := strconv.ParseInt(b.Get("transactionCount").String(), 0, 64)
	txs := b.Get("transactions")
	hash := b.Get("hash").String()
	if link != nil {
		*link = BlockLink{Hash: hash, Parent: b.Get("parent.hash").String()}
	}
	if e1 != nil || e2 != nil || e3 != nil || number != h || hash == "" || !txs.IsArray() || int64(len(txs.Array())) != count {
		return 0, nil, errors.New("incomplete execution GraphQL block")
	}
	var out []Activity
	for i, t := range txs.Array() {
		id := t.Get("hash").String()
		from := t.Get("from.address").String()
		to := t.Get("to.address").String()
		value, ok := new(big.Int).SetString(t.Get("value").String(), 0)
		status, err := strconv.ParseUint(t.Get("status").String(), 0, 8)
		idx, idxErr := strconv.ParseInt(t.Get("index").String(), 0, 64)
		if !ok || value.Sign() < 0 || id == "" || !common.IsHexAddress(from) || (to != "" && !common.IsHexAddress(to)) || err != nil || status > 1 || idxErr != nil || idx != int64(i) || !t.Get("logs").IsArray() {
			return 0, nil, errors.New("incomplete execution GraphQL transaction")
		}
		tx := map[string]any{"from": from, "to": nil, "value": "0x" + value.Text(16), "hash": id, "blockHash": hash, "blockNumber": fmt.Sprintf("0x%x", h), "transactionIndex": fmt.Sprintf("0x%x", idx), "input": t.Get("inputData").String()}
		if to != "" {
			tx["to"] = to
		}
		contract := t.Get("createdContract.address").String()
		ev := Activity{Height: h, TxID: id, TimeMS: stamp * 1000}
		for _, a := range []string{from, to, contract} {
			addAddress(&ev.Addresses, s.n.Name, a)
		}
		logs := []json.RawMessage{}
		evidence := Evidence{Transaction: object(tx)}
		for _, l := range t.Get("logs").Array() {
			topics := l.Get("topics")
			addr := l.Get("account.address").String()
			index, e := strconv.ParseUint(l.Get("index").String(), 0, 64)
			if e != nil || !topics.IsArray() || !common.IsHexAddress(addr) || !l.Get("data").Exists() {
				return 0, nil, errors.New("incomplete GraphQL log")
			}
			logs = append(logs, object(map[string]any{"address": addr, "topics": json.RawMessage(topics.Raw), "data": l.Get("data").String(), "blockHash": hash, "blockNumber": tx["blockNumber"], "transactionHash": id, "transactionIndex": tx["transactionIndex"], "logIndex": fmt.Sprintf("0x%x", index), "removed": false}))
			s.n.indexEVMLog(&ev.Addresses, &evidence, addr, topics.Array(), l)
		}
		evidence.Logs = logs
		evidence.Receipt = object(map[string]any{"blockHash": hash, "blockNumber": tx["blockNumber"], "transactionHash": id, "contractAddress": contract, "status": fmt.Sprintf("0x%x", status), "logs": logs})
		ev.Data = object(evidence)
		out = append(out, ev)
	}
	return h, out, nil
}
