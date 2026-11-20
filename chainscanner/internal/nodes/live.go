package nodes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

// A live queue belongs to a watch period. Pump calls are serialized by the
// store. Workers may outlive a Pump call, but never commit to the store themselves.
type liveQueue struct {
	period     context.Context
	ctx        context.Context
	stop       context.CancelFunc
	detach     func() bool
	workers    sync.WaitGroup
	results    chan liveResult
	running    map[*endpoint]*liveCall
	busy       map[string]bool
	jobs       []*liveJob
	tip        int64
	nextPoll   time.Time
	interval   time.Duration
	probing    int
	nextWarm   time.Time
	warmBudget int
	fresh      bool
}
type liveJob struct {
	span     chains.Span
	active   bool
	tried    map[*endpoint]bool
	failures int
	cause    error
}
type liveCall struct {
	cancel  context.CancelFunc
	done    chan struct{}
	retired bool
}
type liveResult struct {
	node   *endpoint
	job    *liveJob // nil for a head probe
	height int64
	link   chains.BlockLink
	events []chains.Activity
	err    error
}

func (p *SourcePool) stopLive() {
	q := p.live
	if q == nil {
		return
	}
	q.stop()
	q.detach()
	q.workers.Wait()
	p.live = nil
}
func (p *SourcePool) retire(n *endpoint) {
	if q := p.live; q != nil {
		if c := q.running[n]; c != nil {
			c.retired = true
			c.cancel()
			<-c.done
		}
	}
	n.close()
}

// LiveWindow reports reservations still owned by this process. The store
// excludes them when selecting additional durable work, including on retries.
func (p *SourcePool) LiveWindow(ctx, period context.Context, interval time.Duration) (int64, []int64, int, error) {
	if p.live == nil || p.live.period != period {
		p.stopLive()
		live, stop := context.WithCancel(period)
		p.live = &liveQueue{ctx: live, stop: stop, period: period, detach: context.AfterFunc(p.life, stop), tip: -1,
			results: make(chan liveResult, parallelism), running: make(map[*endpoint]*liveCall), busy: make(map[string]bool)}
	}
	q := p.live
	q.interval = interval
	// An idle worker may have woken from WS before its next polling tick.
	if q.tip < 0 || (len(q.jobs) == 0 && q.probing == 0 && !q.fresh) {
		h, err := p.Head(ctx)
		if err != nil {
			return 0, nil, 0, err
		}
		q.tip = p.bestRecentTip(h, interval)
		q.nextPoll = p.now().Add(interval)
	}
	q.fresh = false
	owned := make([]int64, 0, len(q.jobs))
	for _, j := range q.jobs {
		owned = append(owned, j.span.First)
	}
	return q.tip, owned, max(0, p.ScanWindow()-len(q.jobs)), nil
}

func (p *SourcePool) startLive(n *endpoint, j *liveJob) {
	q := p.live
	call, stop := context.WithCancel(q.ctx)
	c := &liveCall{cancel: stop, done: make(chan struct{})}
	q.running[n] = c
	q.busy[n.historyKey] = true
	// The registry can publish a new factory between Pump calls.
	reader := &SourcePool{life: p.life, network: p.network, now: p.now, open: p.open}
	if j == nil {
		q.probing++
	} else {
		j.active = true
		j.tried[n] = true
	}
	q.workers.Go(func() {
		defer close(c.done)
		defer stop()
		r := liveResult{node: n, job: j}
		if j == nil {
			r.height, r.err = reader.height(call, n)
		} else {
			part := segment{from: j.span.First, to: j.span.Last}
			r.err = reader.fetch(call, n, &part, true)
			r.height, r.events = part.to, part.events
			r.link = chains.BlockLink{Hash: part.last.Hash, Parent: part.first.Parent}
		}
		q.results <- r
	})
}

func (p *SourcePool) probeLive() {
	q := p.live
	if q.probing > 0 || len(q.busy) >= parallelism || p.now().Before(q.nextPoll) {
		return
	}
	for k := range p.nodes {
		i := (p.headNext + k) % len(p.nodes)
		n := p.nodes[i]
		if q.busy[n.historyKey] || p.now().Before(n.retry) {
			continue
		}
		p.headNext = (i + 1) % len(p.nodes)
		p.startLive(n, nil)
		return
	}
}

func (q *liveQueue) remove(j *liveJob) {
	i := slices.Index(q.jobs, j)
	q.jobs = slices.Delete(q.jobs, i, i+1)
}

func (p *SourcePool) acceptLive(r liveResult, finish chains.FinishRange) error {
	q := p.live
	c := q.running[r.node]
	<-c.done
	delete(q.running, r.node)
	delete(q.busy, r.node.historyKey)
	j := r.job
	if j == nil {
		q.probing--
		q.nextPoll = p.now().Add(q.interval)
		if c.retired {
			return nil
		}
		p.record(r.node, false, r.err)
		if r.err == nil {
			q.tip = p.bestRecentTip(r.height, q.interval)
			q.fresh = true
		}
		return nil
	}
	j.active = false
	if c.retired {
		return nil
	}
	err := r.err
	if err == nil {
		err = finish(chains.Span{First: j.span.First, Last: r.height}, r.link, r.events, nil)
		if err != nil && !errors.Is(err, chains.ErrFork) {
			return err
		}
	}
	p.record(r.node, true, err)
	if r.node.accepted.err == nil && r.node.accepted.start.After(p.now().Add(-q.interval)) {
		q.tip = p.bestRecentTip(r.node.accepted.height, q.interval)
	}
	if err == nil {
		q.remove(j)
		return nil
	}
	j.cause = r.node.diagnostic(err)
	if !errors.Is(err, errNodeBehind) {
		j.failures++
	}
	return nil
}

// A slot belongs to a provider only after a fresh observation proves that
// its own head covers the entire span at the configured confirmation depth.
func (n *endpoint) covers(last int64, now time.Time, interval time.Duration) bool {
	h := n.accepted
	return h.err == nil && !h.start.IsZero() && now.Sub(h.start) <= 2*interval && h.height >= last && h.height-last >= max(int64(0), n.cfg.Confirmations-1)
}

func (p *SourcePool) candidatesRemain(j *liveJob) bool {
	if j.failures >= 3 {
		return false
	}
	for _, n := range p.nodes {
		if !j.tried[n] && !p.now().Before(n.retry) {
			return true
		}
	}
	return false
}

// Warm spare lanes only for queued work; one provider check can satisfy many
// spans. Even a large discovered inventory has a bounded probe budget.
func (p *SourcePool) warmLive() {
	q := p.live
	now := p.now()
	if !now.Before(q.nextWarm) {
		q.nextWarm = now.Add(q.interval)
		q.warmBudget = parallelism
	}
	pending := 0
	for _, j := range q.jobs {
		if !j.active {
			pending++
		}
	}
	needed := min(pending-q.probing, q.warmBudget, parallelism-len(q.busy))
	if needed <= 0 {
		return
	}
	for _, n := range p.nodes {
		if q.busy[n.historyKey] || now.Before(n.retry) || (!n.accepted.start.IsZero() && now.Sub(n.accepted.start) < q.interval) {
			continue
		}
		for _, j := range q.jobs {
			if !j.active && !j.tried[n] && !n.covers(j.span.Last, now, q.interval) {
				p.startLive(n, nil)
				q.warmBudget--
				needed--
				break
			}
		}
		if needed == 0 {
			return
		}
	}
}

// AdvanceLive returns after individual completions, leaving other leases running.
// A head probe occupies an idle history lane and competes with no active block read.
func (p *SourcePool) AdvanceLive(ctx context.Context, spans []chains.Span, finish chains.FinishRange) (bool, error) {
	q := p.live
	for _, r := range spans {
		q.jobs = append(q.jobs, &liveJob{span: r, tried: make(map[*endpoint]bool)})
	}
	defer p.trimConnections()
	// Prefer the oldest outstanding repair when a provider becomes free.
	slices.SortStableFunc(q.jobs, func(a, b *liveJob) int {
		return cmp.Compare(a.span.First, b.span.First)
	})
	for {
		if err := ctx.Err(); err != nil {
			p.stopLive()
			return false, err
		}
		p.probeLive()
		for _, j := range slices.Clone(q.jobs) {
			if len(q.busy) >= parallelism {
				break
			}
			if j.active {
				continue
			}
			var n *endpoint
			var waiting bool
			if j.failures < 3 {
				n, waiting = p.pickNode(j.span.Last, q.busy, func(n *endpoint) bool { return j.tried[n] || !n.covers(j.span.Last, p.now(), q.interval) })
			}
			if n != nil {
				p.startLive(n, j)
				continue
			}
			if waiting || p.candidatesRemain(j) {
				continue
			}
			err := p.unavailable(fmt.Sprintf("range %d..%d", j.span.First, j.span.Last), j.cause)
			if err = finish(j.span, chains.BlockLink{}, nil, err); err != nil {
				p.stopLive()
				return false, err
			}
			q.remove(j)
		}
		p.warmLive()
		if len(q.running) == 0 {
			return false, nil
		}
		// If all lanes are occupied, the next completion offers a lane to the due probe.
		var tick <-chan time.Time
		if q.probing == 0 && p.now().Before(q.nextPoll) {
			tick = time.After(q.nextPoll.Sub(p.now()))
		}
		var wake <-chan struct{}
		if q.probing == 0 && tick != nil {
			wake = p.Wakeups()
		}
		select {
		case <-ctx.Done():
			p.stopLive()
			return false, ctx.Err()
		case <-q.ctx.Done():
			p.stopLive()
			return false, context.Canceled
		case <-tick:
			continue
		case <-wake:
			q.nextPoll = p.now()
			continue
		case r := <-q.results:
			if err := p.acceptLive(r, finish); err != nil {
				p.stopLive()
				return false, err
			}
			// A completion frees capacity immediately, even with older requests outstanding.
			return true, nil
		}
	}
}
