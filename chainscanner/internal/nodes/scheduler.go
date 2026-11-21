package nodes

import (
	"context"
	"errors"
	"fmt"
	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"sync"
	"time"
)

// ScanWindow preserves the multi-node lookahead in units of durable work.
func (p *SourcePool) ScanWindow() int {
	active := map[string]bool{}
	for _, n := range p.nodes {
		if !n.fallback && !p.now().Before(n.retry) {
			active[n.historyKey] = true
		}
		if len(active) == parallelism {
			break
		}
	}
	return max(2, len(active)*rangesPerLane)
}

type pendingRange struct {
	segment
	tried       map[*endpoint]bool
	slowHistory string
	failures    int
	cause       error
}

func (j *pendingRange) excluded(n *endpoint) bool {
	return j.tried[n] || n.historyKey == j.slowHistory
}

// ScanPending serves the store's sparse work list. The legacy Scan/Read API
// still returns contiguous data for callers that do not own a gap store.
func (p *SourcePool) ScanPending(ctx context.Context, spans []chains.Span, finish func(chains.Span, chains.BlockLink, []chains.Activity, error) error) error {
	defer p.trimConnections()

	jobs := make([]pendingRange, len(spans))
	for i, r := range spans {
		jobs[i] = pendingRange{segment: segment{from: r.First, to: r.Last}, tried: map[*endpoint]bool{}}
	}
	calls, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	busy := map[string]bool{}
	results := make(chan completion, parallelism)
	for remaining := len(jobs); remaining > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		oldestIndex := -1
		var oldest *pendingRange
		for i := range jobs {
			j := &jobs[i]
			if !j.done && (oldest == nil || j.from < oldest.from) {
				oldestIndex, oldest = i, j
			}
		}

		for slot := range jobs {
			i := slot
			if slot == 0 {
				i = oldestIndex
			} else if slot <= oldestIndex {
				i = slot - 1
			}
			if oldest.yielding || len(busy) >= parallelism {
				break
			}
			j := &jobs[i]
			if j.running || j.done {
				continue
			}
			node, waiting := p.pickRange(spans[i], j.failures, busy, j.excluded)
			if node == nil {
				if waiting {
					continue
				}
				failure := p.unavailable(fmt.Sprintf("range %d..%d", j.from, j.to), j.cause)
				if err := finish(spans[i], chains.BlockLink{}, nil, failure); err != nil {
					return err
				}
				j.done = true
				remaining--
				continue
			}
			busy[node.historyKey] = true
			j.tried[node] = true
			p.start(calls, &wg, results, i, node, &j.segment, true)
		}
		if remaining == 0 {
			break
		}
		if oldest.done {
			continue
		}
		var checkGap <-chan time.Time
		if oldest.failures < 3 {
			checkGap = p.checkSlowGap(&oldest.segment, spans[oldestIndex].Last, busy, oldest.excluded)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-checkGap:
			continue
		case result := <-results:
			j := &jobs[result.slot]
			delete(busy, result.node.historyKey)
			j.running, j.yielding = false, false
			j.cancel = nil
			if ctx.Err() != nil {
				return ctx.Err()
			}
			err := result.err
			if err == nil {
				link := chains.BlockLink{Hash: j.last.Hash, Parent: j.first.Parent}
				err = finish(chains.Span{First: j.from, Last: j.to}, link, j.events, nil)
				if err != nil && !errors.Is(err, chains.ErrFork) {
					return err
				}
			}
			if err == errSlowGap {
				j.slowHistory = result.node.historyKey
			} else {
				p.record(result.node, true, err)
				if err != nil {
					j.cause = result.node.diagnostic(err)
					if !errors.Is(err, errNodeBehind) {
						j.failures++
					}
				}
			}
			j.events = nil
			if err == nil {
				j.done = true
				remaining--
			} else {
				j.to = spans[result.slot].Last
			}
		}
	}
	return nil
}
