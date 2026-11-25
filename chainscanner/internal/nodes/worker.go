package nodes

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

type segment struct {
	readStarted                 bool // this work item has already entered a block read
	behind                      map[*endpoint]bool
	from, to                    int64
	running, done               bool
	retry, redirected, yielding bool
	started                     time.Time
	cancel                      context.CancelCauseFunc
	events                      []chains.Activity
	first, last                 chains.BlockLink
	owner                       *endpoint
	bufferedBytes               int64
}

func (p *SourcePool) fetch(ctx context.Context, n *endpoint, j *segment, oneRead bool) error {
	call, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	defer p.lanes.scan(chains.ProviderKey(n.cfg))()
	required := j.to + max(n.cfg.Confirmations-1, 0)
	if n.tip < required || n.src == nil {
		h, err := p.readHeight(call, n)
		if err != nil {
			return err
		}
		if h < required {
			return errNodeBehind
		}
	}
	// Count a provider's range read only after its head is eligible. A single
	// attempt may require several RPCs to read the range.
	p.scanAttempts.record(j.readStarted)
	j.readStarted = true
	j.events = nil
	j.first, j.last = chains.BlockLink{}, chains.BlockLink{}
	for start := j.from; start <= j.to; {
		part, link, rows, err := chains.ReadSpan(call, p.network, n.src, chains.Span{First: start, Last: j.to})
		if err != nil {
			return err
		}
		if start == j.from {
			j.first = link
		} else if j.last.Hash != link.Parent {
			return errors.New("inconsistent block ancestry")
		}
		j.last = link
		end := part.Last
		if len(rows) > 0 {
			sources := chains.SourceIDs(n.cfg)
			for i := range rows {
				rows[i].ScanSources = sources
			}
		}
		j.events = append(j.events, rows...)
		if oneRead {
			j.to = end
			break
		}
		start = end + 1
	}
	return nil
}

type completion struct {
	slot int
	node *endpoint
	err  error
}

var errSlowGap = errors.New("slow range reassigned")

func (p *SourcePool) start(ctx context.Context, wg *sync.WaitGroup, results chan<- completion, slot int, node *endpoint, j *segment, oneRead bool) {
	attempt, stop := context.WithCancelCause(ctx)
	j.running, j.started, j.cancel = true, p.now(), stop
	wg.Go(func() {
		err := p.fetch(attempt, node, j, oneRead)
		if err != nil && context.Cause(attempt) == errSlowGap {
			err = errSlowGap
		}
		stop(nil)
		results <- completion{slot, node, err}
	})
}

// Yield at most once, and only when a distinct eligible provider is idle.
func (p *SourcePool) checkSlowGap(j *segment, last int64, busy map[string]bool, excluded func(*endpoint) bool) <-chan time.Time {
	if !j.running || j.redirected || len(busy) >= parallelism {
		return nil
	}
	now := p.now()
	var wait time.Duration
	for _, peer := range p.nodes {
		if peer.fallback || busy[peer.historyKey] || excluded(peer) || now.Before(peer.retry) {
			continue
		}
		if left := slowGapDelay - now.Sub(j.started); left > 0 {
			return time.After(left)
		}
		if peer.recentlyBehind(last, now) {
			left := time.Second - now.Sub(peer.observed)
			if wait == 0 || left < wait {
				wait = left
			}
			continue
		}
		j.redirected, j.yielding = true, true
		j.cancel(errSlowGap)
		return nil
	}
	if wait > 0 {
		return time.After(wait)
	}
	return nil
}

// A recent head below the requested confirmation height is a routing hint.
// Recheck it if all idle candidates are behind; a fast chain may have advanced.
func (n *endpoint) recentlyBehind(last int64, now time.Time) bool {
	return n.tip >= 0 && n.tip < last+max(n.cfg.Confirmations-1, 0) && now.Sub(n.observed) < time.Second
}

// Fresh ranges start on ordinary nodes. After a failed attempt, prefer a free
// repair endpoint; other healthy providers can still make progress if it is busy.
func (p *SourcePool) pickRange(span chains.Span, failures int, busy map[string]bool, excluded func(*endpoint) bool) (*endpoint, bool) {
	var repairBusy bool
	if span.Failed || failures > 0 {
		n, waiting := p.pickRole(span.Last, busy, excluded, true)
		if n != nil {
			return n, false
		}
		repairBusy = waiting
	}
	if failures >= 3 {
		return nil, repairBusy
	}
	n, waiting := p.pickNode(span.Last, busy, excluded)
	return n, waiting || repairBusy
}

// Round-robin across distinct history backends. The boolean reports that an
// otherwise eligible backend is currently serving another request.
func (p *SourcePool) pickNode(last int64, busy map[string]bool, excluded func(*endpoint) bool) (*endpoint, bool) {
	return p.pickRole(last, busy, excluded, false)
}

func (p *SourcePool) pickRole(last int64, busy map[string]bool, excluded func(*endpoint) bool, fallback bool) (*endpoint, bool) {
	selected := -1
	waiting := false
	now := p.now()
	cursor := &p.next
	if fallback {
		cursor = &p.repairNext
	}
	for offset := range p.nodes {
		i := (*cursor + offset) % len(p.nodes)
		n := p.nodes[i]
		if n.fallback != fallback || excluded(n) || now.Before(n.retry) {
			continue
		}
		if busy[n.historyKey] {
			waiting = true
			continue
		}
		if selected < 0 {
			selected = i
		}
		if !n.recentlyBehind(last, now) {
			selected = i
			break
		}
	}
	if selected < 0 {
		return nil, waiting
	}
	n := p.nodes[selected]
	n.picks++
	if n.picks >= max(1, n.weight) {
		n.picks = 0
		*cursor = (selected + 1) % len(p.nodes)
	} else {
		*cursor = selected
	}
	return p.nodes[selected], waiting
}
