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
	var out []Activity
	for h := start; h <= end; h++ {
		if !window.produced[h-window.start] {
			continue
		}
		b, err := s.call(ctx, "getBlock", h, map[string]any{"commitment": "finalized", "encoding": "jsonParsed", "transactionDetails": "full", "rewards": false, "maxSupportedTransactionVersion": 1})
		if err != nil {
			return 0, nil, err
		}
		if b.Get("blockhash").String() == "" || !b.Get("transactions").IsArray() {
			return 0, nil, errors.New("incomplete Solana block")
		}
		for _, tx := range b.Get("transactions").Array() {
			id := tx.Get("transaction.signatures.0").String()
			if id == "" {
				return 0, nil, errors.New("missing Solana signature")
			}
			e := Activity{Height: h, TxID: id, TimeMS: b.Get("blockTime").Int() * 1000, Data: json.RawMessage(tx.Raw)}
			for _, k := range tx.Get("transaction.message.accountKeys").Array() {
				addAddress(&e.Addresses, "solana", k.Get("pubkey").String())
			}
			for _, p := range []string{"meta.preTokenBalances", "meta.postTokenBalances"} {
				for _, bal := range tx.Get(p).Array() {
					addAddress(&e.Addresses, "solana", bal.Get("owner").String())
				}
			}
			out = append(out, e)
		}
	}
	return end, out, nil
}
