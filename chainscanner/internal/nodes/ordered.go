package nodes

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

// Scan dispatches more work after each completion. A slow gap can hold up
// delivery, but other endpoints keep fetching up to the reorder buffer limit.
// emit runs serially on complete, consecutive ranges while later RPCs run.
func (p *SourcePool) Scan(ctx context.Context, next, head int64, emit func(int64, []chains.Activity) error) error {
	return p.scan(ctx, next, head, 2*rangesPerLane*parallelism, emit)
}

func (p *SourcePool) scan(ctx context.Context, next, head int64, maxRanges int, emit func(int64, []chains.Activity) error) error {
	defer p.trimConnections()
	if next < 0 || next > head {
		return errors.New("invalid scan window")
	}
	distinct := map[string]bool{}
	for _, n := range p.nodes {
		if !n.fallback && !p.now().Before(n.retry) {
			distinct[n.historyKey] = true
		}
	}
	lanes := min(parallelism, len(distinct))
	if lanes == 0 {
		return p.unavailable("range", nil)
	}
	width := int64(4)
	switch p.network {
	case "solana":
		width = 16
	case "aptos":
		width = 100
	}
	remaining := head - next + 1
	if remaining <= int64(2*lanes) {
		width = 1
	} else {
		width = min(width, remaining/int64(lanes))
	}
	window := rangesPerLane * lanes
	// Periodically return to the caller for updated configuration and tip.
	maxRanges = min(maxRanges, 2*window)
	if lanes == 1 {
		maxRanges = min(maxRanges, 4)
	}
	var jobs []segment
	for h := next; h <= head && len(jobs) < maxRanges; {
		end := h + min(width-1, head-h)
		jobs = append(jobs, segment{from: h, to: end})
		h = end + 1
	}
	results := make(chan completion, parallelism)
	fetchCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	busy := map[string]bool{}
	failed := map[*endpoint]bool{}
	previous := ""
	if p.anchorHeight == next-1 {
		previous = p.anchorHash
	}
	var bufferedBytes int64
	var failure error
	for front := 0; front < len(jobs); {
		if err := ctx.Err(); err != nil {
			return err
		}
		gap := &jobs[front]
		// First unassigned slot has priority. A failed slot is immediately
		// eligible again, on any peer that has not failed this scan.
		for slot := front; slot < min(len(jobs), front+window) && len(busy) < lanes; slot++ {
			j := &jobs[slot]
			if j.running || j.done {
				continue
			}
			// Reserve a replacement for a cancelled front read; its result
			// will return through the same channel as any other attempt.
			if gap.yielding {
				break
			}
			if slot != front && bufferedBytes >= reorderBytes && !j.retry {
				continue
			}
			worker, _ := p.pickNode(j.to, busy, func(n *endpoint) bool { return failed[n] || j.behind[n] })
			if worker == nil {
				break
			}
			busy[worker.historyKey] = true
			p.start(fetchCtx, &wg, results, slot, worker, j, false)
		}
		if len(busy) == 0 {
			return p.unavailable(fmt.Sprintf("range %d..%d", gap.from, gap.to), failure)
		}
		checkGap := p.checkSlowGap(gap, gap.to, busy, func(n *endpoint) bool { return failed[n] || gap.behind[n] })
		var result completion
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-checkGap:
			continue
		case result = <-results:
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		j := &jobs[result.slot]
		delete(busy, result.node.historyKey)
		j.running = false
		j.cancel = nil
		j.yielding = false
		if result.err == errSlowGap {
			for _, peer := range p.nodes {
				if peer.historyKey == result.node.historyKey {
					failed[peer] = true
				}
			}
		} else {
			p.record(result.node, true, result.err)
		}
		if result.err != nil {
			failure = result.node.diagnostic(result.err)
			j.retry = true
			j.events = nil
			if errors.Is(result.err, errNodeBehind) {
				if j.behind == nil {
					j.behind = make(map[*endpoint]bool)
				}
				j.behind[result.node] = true
			} else {
				failed[result.node] = true
			}
			continue
		}
		j.done = true
		j.owner = result.node
		if result.slot != front {
			j.bufferedBytes = activityBytes(j.events)
			bufferedBytes += j.bufferedBytes
		}
		for front < len(jobs) && jobs[front].done {
			ready := &jobs[front]
			bufferedBytes -= ready.bufferedBytes
			ready.bufferedBytes = 0
			if previous != "" && previous != ready.first.Parent {
				// Another node may have a different view at the tip. Re-read
				// only this range against the already emitted predecessor.
				failed[ready.owner] = true
				ready.done = false
				ready.retry = true
				ready.events = nil
				break
			}
			if err := emit(ready.to, ready.events); err != nil {
				return err
			}
			previous = ready.last.Hash
			p.anchorHeight, p.anchorHash = ready.to, previous
			ready.events = nil
			front++
		}
	}
	return nil
}

func (p *SourcePool) Read(ctx context.Context, next, head int64) (int64, []chains.Activity, error) {
	var end int64
	var events []chains.Activity
	// Read collects all rows in one slice. Keep this compatibility API bounded;
	// the scan worker uses Scan to stream the longer window.
	err := p.scan(ctx, next, head, 4*parallelism, func(h int64, rows []chains.Activity) error { end = h; events = append(events, rows...); return nil })
	if err != nil {
		return 0, nil, err
	}
	return end, events, nil
}

// Estimate retained decoded activity data without serializing it again.
func activityBytes(events []chains.Activity) int64 {
	size := int64(cap(events)) * 128
	for i := range events {
		ev := &events[i]
		size += int64(cap(ev.Data)) + int64(len(ev.TxID)) + int64(len(ev.Network)) + int64(cap(ev.Addresses))*16
		for _, a := range ev.Addresses {
			size += int64(len(a))
		}
	}
	return size
}
