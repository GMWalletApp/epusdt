package chains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"
)

// Head notifications only wake the scheduler. The read endpoint supplies
// the chain ID, authoritative height and block contents.
func evmHeads(ctx context.Context, c *rpc.Client, observe func(int64)) error {
	headers := make(chan struct{ Number hexutil.Uint64 }, 8)
	setup, cancel := context.WithTimeout(ctx, 8*time.Second)
	sub, err := c.EthSubscribe(setup, headers, "newHeads")
	cancel()
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sub.Err():
			return err
		case h := <-headers:
			if height := int64(h.Number); height > 0 {
				observe(height)
			}
		}
	}
}

// BlockLink identifies the exact EVM block whose transactions were decoded.
type BlockLink struct{ Hash, Parent string }

func (s *jsonSource) ReadLinkedBlock(ctx context.Context, h int64, link *BlockLink) (int64, []Activity, error) {
	if s.n.Name == "bitcoin" {
		return s.bitcoinBlock(ctx, h, link)
	}
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
	if link != nil {
		*link = BlockLink{Hash: blockHash, Parent: block.Get("parentHash").String()}
	}
	receipts := map[string]gjson.Result{}
	readReceipts := func() error {
		rows, err := s.call(ctx, "eth_getBlockReceipts", label)
		if err != nil {
			return err
		}
		txs := block.Get("transactions").Array()
		if !rows.IsArray() || len(rows.Array()) != len(txs) {
			return errors.New("incomplete EVM receipts")
		}
		for i, r := range rows.Array() {
			id := txs[i].Get("hash").String()
			if r.Get("blockHash").String() != blockHash || r.Get("transactionHash").String() != id || !r.Get("logs").IsArray() {
				return errors.New("EVM receipt mismatch")
			}
			receipts[id] = r
			byHash[id] = r.Get("logs").Array()
		}
		return nil
	}
	// Whole-block receipts supply both log evidence and transaction status.
	// Providers without this method can still serve individual receipts.
	attemptedReceipts := NativeAsset(s.n.Name) != ""
	var receiptErr error
	if attemptedReceipts {
		receiptErr = readReceipts()
		var status rpc.HTTPError
		if errors.As(receiptErr, &status) && status.StatusCode == 429 {
			return 0, nil, receiptErr
		}
	}
	if !attemptedReceipts || receiptErr != nil {
		clear(receipts)
		clear(byHash)
		logs, err := s.call(ctx, "eth_getLogs", map[string]string{"blockHash": blockHash})
		if err != nil {
			var status rpc.HTTPError
			if attemptedReceipts || (errors.As(err, &status) && status.StatusCode == 429) {
				return 0, nil, err
			}
			if err = readReceipts(); err != nil {
				return 0, nil, err
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
			s.n.indexEVMLog(&e.Addresses, &data, l.Get("address").String(), l.Get("topics").Array(), l)
			data.Logs = append(data.Logs, json.RawMessage(l.Raw))
		}
		if trace, ok := traces[id]; ok {
			data.Trace = json.RawMessage(trace.Raw)
			traceAddresses(&e.Addresses, s.n.Name, trace)
		}
		creation := strings.TrimSpace(tx.Get("to").String()) == ""
		value := tx.Get("value").String()
		if creation || (NativeAsset(s.n.Name) != "" && value != "" && value != "0x0") {
			r, ok := receipts[id]
			if !ok {
				r, err = s.call(ctx, "eth_getTransactionReceipt", id)
				if err != nil {
					return 0, nil, err
				}
			}
			if r.Get("blockHash").String() != blockHash || r.Get("transactionHash").String() != id {
				return 0, nil, errors.New("receipt transaction or block mismatch")
			}
			if creation {
				addAddress(&e.Addresses, s.n.Name, r.Get("contractAddress").String())
			}
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

// Fetch a bounded window concurrently, then hand it to SQLite in ledger order.
// A partial window is retried from the durable cursor.
func (s *jsonSource) evmRange(ctx context.Context, start, head int64) (int64, []Activity, error) {
	end := min(head, start+3)
	if end == start {
		return s.ReadLinkedBlock(ctx, start, nil)
	}
	group, ctx := errgroup.WithContext(ctx)
	blocks := make([][]Activity, end-start+1)
	for h := start; h <= end; h++ {
		group.Go(func() error { _, events, err := s.ReadLinkedBlock(ctx, h, nil); blocks[h-start] = events; return err })
	}
	if err := group.Wait(); err != nil {
		return 0, nil, err
	}
	var out []Activity
	for _, events := range blocks {
		out = append(out, events...)
	}
	return end, out, nil
}
