package chains

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
)

// Only finalized slot membership is cached. Block payloads and delivery boundaries
// remain per requested span, even when a neighboring worker is delayed.
type slotWindow struct {
	start, end int64
	produced   [16]bool
}

func (s *jsonSource) finalizedSlots(ctx context.Context, start, end int64) (*slotWindow, error) {
	if w := s.slots.Load(); w != nil && w.start <= start && end <= w.end {
		return w, nil
	}
	w := &slotWindow{start: start, end: start + min(int64(15), max(end, s.finalizedSlot.Load())-start)}
	slots, err := s.call(ctx, "getBlocks", start, w.end, map[string]string{"commitment": "finalized"})
	if err != nil {
		return nil, err
	}
	if !slots.IsArray() {
		return nil, errors.New("missing finalized Solana slots")
	}
	prev := start - 1
	for _, v := range slots.Array() {
		h, err := strconv.ParseInt(v.Raw, 10, 64)
		if err != nil || h <= prev || h > w.end {
			return nil, errors.New("invalid finalized Solana slot list")
		}
		w.produced[h-start] = true
		prev = h
	}
	if !w.produced[0] {
		first, err := s.call(ctx, "getFirstAvailableBlock")
		if err != nil {
			return nil, err
		}
		earliest, err := strconv.ParseInt(first.Raw, 10, 64)
		if err != nil || earliest < 0 || earliest > start {
			return nil, errors.New("Solana history pruned or unavailable before saved cursor")
		}
	}
	s.slots.Store(w)
	return w, nil
}
func (s *jsonSource) solana(ctx context.Context, start, head int64) (int64, []Activity, error) {
	end := min(start+15, head)
	window, err := s.finalizedSlots(ctx, start, end)
	if err != nil {
		return 0, nil, err
	}
	read := func(ctx context.Context, first, last int64) (int64, []Activity, error) {
		var out []Activity
		for h := first; h <= last; h++ {
			if !window.produced[h-window.start] {
				continue
			}
			events, err := s.solanaBlock(ctx, h)
			if err != nil {
				return 0, nil, err
			}
			out = append(out, events...)
		}
		return last, out, nil
	}
	if end == start+1 && s.catchup.enabled() && window.produced[start-window.start] && window.produced[end-window.start] {
		return s.catchup.read(ctx, start, end, end, read)
	}
	return read(ctx, start, end)
}

func (s *jsonSource) solanaBlock(ctx context.Context, h int64) ([]Activity, error) {
	b, err := s.call(ctx, "getBlock", h, map[string]any{"commitment": "finalized", "encoding": "jsonParsed", "transactionDetails": "full", "rewards": false, "maxSupportedTransactionVersion": 1})
	if err != nil {
		return nil, err
	}
	if b.Get("blockhash").String() == "" || !b.Get("transactions").IsArray() {
		return nil, errors.New("incomplete Solana block")
	}
	stamp := b.Get("blockTime").Int() * 1000
	txs := b.Get("transactions").Array()
	out := make([]Activity, 0, len(txs))
	cache := make(addressCache)
	for _, tx := range txs {
		id := tx.Get("transaction.signatures.0").String()
		if id == "" {
			return nil, errors.New("missing Solana signature")
		}
		e := Activity{Height: h, TxID: id, TimeMS: stamp, Data: json.RawMessage(tx.Raw)}
		for _, k := range tx.Get("transaction.message.accountKeys").Array() {
			cache.add(&e.Addresses, "solana", k.Get("pubkey").String())
		}
		for _, p := range []string{"meta.preTokenBalances", "meta.postTokenBalances"} {
			for _, bal := range tx.Get(p).Array() {
				cache.add(&e.Addresses, "solana", bal.Get("owner").String())
			}
		}
		out = append(out, e)
	}
	return out, nil
}
