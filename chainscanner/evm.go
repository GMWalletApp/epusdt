package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tidwall/gjson"
	"strconv"
	"strings"
)

func (s *jsonSource) evm(ctx context.Context, h int64) (int64, []Activity, error) {
	label := fmt.Sprintf("0x%x", h)
	block, err := s.call(ctx, "eth_getBlockByNumber", label, true)
	if err != nil {
		return 0, nil, err
	}
	if block.Get("number").String() != label || !block.Get("transactions").IsArray() || block.Get("hash").String() == "" {
		return 0, nil, errors.New("incomplete EVM block")
	}
	stamp, err := strconv.ParseInt(block.Get("timestamp").String(), 0, 64)
	if err != nil {
		return 0, nil, err
	}
	byHash := map[string][]gjson.Result{}
	blockHash := block.Get("hash").String()
	logs, err := s.call(ctx, "eth_getLogs", map[string]string{"blockHash": blockHash})
	if err != nil {
		// Some providers restrict blockHash log queries but serve full block receipts.
		receipts, re := s.call(ctx, "eth_getBlockReceipts", label)
		if re != nil {
			return 0, nil, fmt.Errorf("block logs: %w; receipts: %v", err, re)
		}
		txs := block.Get("transactions").Array()
		if !receipts.IsArray() || len(receipts.Array()) != len(txs) {
			return 0, nil, errors.New("incomplete EVM receipts")
		}
		for i, r := range receipts.Array() {
			if r.Get("blockHash").String() != blockHash || r.Get("transactionHash").String() != txs[i].Get("hash").String() || !r.Get("logs").IsArray() {
				return 0, nil, errors.New("EVM receipt mismatch")
			}
			byHash[r.Get("transactionHash").String()] = r.Get("logs").Array()
		}
	} else {
		if !logs.IsArray() {
			return 0, nil, errors.New("missing EVM logs")
		}
		for _, l := range logs.Array() {
			if l.Get("removed").Bool() || l.Get("blockHash").String() != blockHash {
				return 0, nil, errors.New("EVM log block mismatch")
			}
			id := l.Get("transactionHash").String()
			byHash[id] = append(byHash[id], l)
		}
	}
	traces := map[string]gjson.Result{}
	if s.n.TraceCalls {
		frames, err := s.call(ctx, "debug_traceBlockByHash", block.Get("hash").String(), map[string]string{"tracer": "callTracer"})
		if err != nil {
			return 0, nil, err
		}
		if !frames.IsArray() || len(frames.Array()) != len(block.Get("transactions").Array()) {
			return 0, nil, errors.New("incomplete EVM call traces")
		}
		for i, f := range frames.Array() {
			if !f.Get("result").IsObject() {
				return 0, nil, errors.New("missing EVM call frame")
			}
			traces[block.Get(fmt.Sprintf("transactions.%d.hash", i)).String()] = f.Get("result")
		}
	}
	var out []Activity
	for _, tx := range block.Get("transactions").Array() {
		id := tx.Get("hash").String()
		if id == "" {
			return 0, nil, errors.New("missing EVM transaction hash")
		}
		e := Activity{Height: h, TxID: id, TimeMS: stamp * 1000}
		data := Evidence{Transaction: json.RawMessage(tx.Raw)}
		addAddress(&e.Addresses, s.n.Name, tx.Get("from").String())
		addAddress(&e.Addresses, s.n.Name, tx.Get("to").String())
		for _, l := range byHash[id] {
			addAddress(&e.Addresses, s.n.Name, l.Get("address").String())
			if topics := l.Get("topics").Array(); len(topics) > 1 {
				indexTopics(&e.Addresses, s.n.Name, topics[1:])
			}
			data.Logs = append(data.Logs, json.RawMessage(l.Raw))
		}
		if trace, ok := traces[id]; ok {
			data.Trace = json.RawMessage(trace.Raw)
			traceAddresses(&e.Addresses, s.n.Name, trace)
		}
		if strings.TrimSpace(tx.Get("to").String()) == "" {
			r, err := s.call(ctx, "eth_getTransactionReceipt", id)
			if err != nil {
				return 0, nil, err
			}
			if r.Get("blockHash").String() != block.Get("hash").String() {
				return 0, nil, errors.New("missing creation receipt")
			}
			addAddress(&e.Addresses, s.n.Name, r.Get("contractAddress").String())
			data.Receipt = json.RawMessage(r.Raw)
		}
		e.Data = object(data)
		out = append(out, e)
	}
	return h, out, nil
}
func traceAddresses(a *[]string, n string, t gjson.Result) {
	addAddress(a, n, t.Get("from").String())
	addAddress(a, n, t.Get("to").String())
	for _, child := range t.Get("calls").Array() {
		traceAddresses(a, n, child)
	}
}
