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

const (
	parallelism   = 8
	rangesPerLane = 4
	reorderBytes  = 32 << 20
	slowGapDelay  = 10 * time.Second
)

// SourcePool is owned by one scan loop. SetNodes, Head, Read and Close must be
// called serially. Independent nodes within a Read are used concurrently.
type SourcePool struct {
	uses           uint64
	life           context.Context
	network        string
	nodes          []*endpoint
	next, headNext int
	anchorHeight   int64
	anchorHash     string
	open           func(context.Context, chains.Network) (chains.Source, error)
	now            func() time.Time
	// Report receives completed provider attempts. Reports from a Read are serialized.
	Report func(chains.Network, bool)
}

type endpoint struct {
	lastUse                    uint64
	cfg                        chains.Network
	src                        chains.Source
	stop                       context.CancelFunc
	tip                        int64
	headFailures, readFailures failureStreak
	historyKey                 string
	retry                      time.Time
}

// Count consecutive failures of the same operation. Each group of three
// increases its penalty until a successful operation resets that streak.
type failureStreak struct{ attempts, level int }

func (s *failureStreak) fail() time.Duration {
	s.attempts++
	if s.attempts < 3 {
		return 0
	}
	s.attempts = 0
	s.level = min(s.level+1, 11)
	return time.Duration(min(60, s.level*(s.level+1)/2)) * time.Minute
}

func NewSourcePool(ctx context.Context, network string) *SourcePool {
	return &SourcePool{life: ctx, network: network, open: chains.NewSource, now: time.Now}
}

// SetNodes retains connections and cooldowns across refreshes. Two transports
// using the same history URL share one read lane.
func (p *SourcePool) SetNodes(configs []chains.Network) error {
	old := make(map[chains.Network]*endpoint, len(p.nodes))
	for _, n := range p.nodes {
		old[n.cfg] = n
	}
	type route struct {
		endpoint NodeEndpoint
		history  string
	}
	seen := map[route]bool{}
	var next []*endpoint
	for _, cfg := range configs {
		if cfg.Name != p.network || !chains.Supported(cfg.Name) {
			return errors.New("scan node must specify the pool's concrete network")
		}
		if err := chains.ValidateNetwork(cfg); err != nil {
			return err
		}
		protocol, _ := chains.EndpointProtocol(cfg)
		key := route{(NodeEndpoint{cfg.Name, protocol, cfg.URL}).identity(), canonicalNodeURL(cfg.HistoryURL)}
		if seen[key] {
			continue
		}
		seen[key] = true
		n := old[cfg]
		if n == nil {
			history := cfg.HistoryURL
			if history == "" {
				history = cfg.URL
			}
			n = &endpoint{cfg: cfg, tip: -1, historyKey: canonicalNodeURL(history)}
		}
		next = append(next, n)
	}
	for _, n := range next {
		delete(old, n.cfg)
	}
	for _, n := range old {
		n.close()
	}
	p.nodes = next
	return nil
}
func (n *endpoint) close() {
	if n.stop != nil {
		n.stop()
		n.stop = nil
	}
	if n.src != nil {
		n.src.Close()
		n.src = nil
	}
	n.tip = -1
}
func (p *SourcePool) Close() {
	for _, n := range p.nodes {
		n.close()
	}
}

// Discovery can provide many URLs. Retain the recently used connections
// while preserving cooldowns for every candidate in the registry.
func (p *SourcePool) trimConnections() {
	var live []*endpoint
	for _, n := range p.nodes {
		if n.src != nil {
			live = append(live, n)
		}
	}
	if len(live) <= 2*parallelism {
		return
	}
	slices.SortFunc(live, func(a, b *endpoint) int { return cmp.Compare(b.lastUse, a.lastUse) })
	for _, n := range live[2*parallelism:] {
		n.close()
	}
}

func (p *SourcePool) Wakeups() <-chan struct{} {
	for _, n := range p.nodes {
		if ch := chains.Wakeups(n.src); ch != nil {
			return ch
		}
	}
	return nil
}
func (p *SourcePool) connect(ctx context.Context, n *endpoint) error {
	if n.src != nil {
		return nil
	}
	// The dial is bounded, but a successful streaming connection lives for
	// the watch period, independently of the individual read deadline.
	live, stop := context.WithCancel(p.life)
	timer := time.AfterFunc(15*time.Second, stop)
	detach := context.AfterFunc(ctx, stop)
	s, err := p.open(live, n.cfg)
	timedOut := !timer.Stop()
	detach()
	if err != nil || timedOut || live.Err() != nil {
		stop()
		if s != nil {
			s.Close()
		}
		return errors.New("scan node connection failed")
	}
	n.src, n.stop = s, stop
	return nil
}
func (p *SourcePool) record(n *endpoint, read bool, err error) {
	counter := &n.headFailures
	if read {
		counter = &n.readFailures
	}
	if err == nil {
		p.uses++
		n.lastUse = p.uses
		*counter = failureStreak{}
		if read {
			n.headFailures = failureStreak{}
		}
		if n.headFailures == (failureStreak{}) && n.readFailures == (failureStreak{}) {
			n.retry = time.Time{}
		}
	} else {
		if cooldown := counter.fail(); cooldown > 0 {
			n.retry = p.now().Add(cooldown)
		}
		n.close()
	}
	if p.Report != nil && (read || err != nil || n.readFailures == (failureStreak{})) {
		p.Report(n.cfg, err == nil)
	}
}
func (p *SourcePool) height(ctx context.Context, n *endpoint) (int64, error) {
	if err := p.connect(ctx, n); err != nil {
		return 0, err
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	h, err := n.src.Head(call)
	if err == nil && h < 0 {
		err = errors.New("invalid node height")
	}
	if err == nil {
		n.tip = h
	}
	return h, err
}
func (p *SourcePool) Head(ctx context.Context) (int64, error) {
	defer p.trimConnections()
	for k := range p.nodes {
		i := (p.headNext + k) % len(p.nodes)
		n := p.nodes[i]
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if p.now().Before(n.retry) {
			continue
		}
		h, err := p.height(ctx, n)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		p.record(n, false, err)
		if err == nil {
			p.headNext = (i + 1) % len(p.nodes)
			return h, nil
		}
	}
	return 0, errors.New("no scan node available for height (failed or cooling down)")
}

type segment struct {
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

func (p *SourcePool) fetch(ctx context.Context, n *endpoint, j *segment, oneRead bool) error {
	call, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	required := j.to + max(n.cfg.Confirmations-1, 0)
	if n.tip < required || n.src == nil {
		h, err := p.height(call, n)
		if err != nil {
			return err
		}
		if h < required {
			return errors.New("node has not confirmed this range")
		}
	}
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

// Yield a slow request only if some later work succeeded and a distinct
// eligible provider is idle. Each span may yield once.
func (p *SourcePool) checkSlowGap(j *segment, busy map[string]bool, excluded func(*endpoint) bool) <-chan time.Time {
	if !j.running || j.redirected || len(busy) >= parallelism {
		return nil
	}
	for _, peer := range p.nodes {
		if busy[peer.historyKey] || excluded(peer) || p.now().Before(peer.retry) {
			continue
		}
		if left := slowGapDelay - p.now().Sub(j.started); left > 0 {
			return time.After(left)
		}
		j.redirected, j.yielding = true, true
		j.cancel(errSlowGap)
		break
	}
	return nil
}

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
		if !p.now().Before(n.retry) {
			distinct[n.historyKey] = true
		}
	}
	lanes := min(parallelism, len(distinct))
	if lanes == 0 {
		return errors.New("all scan nodes unavailable")
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
	var readyCount int
	for front := 0; front < len(jobs); {
		if err := ctx.Err(); err != nil {
			return err
		}
		var checkGap <-chan time.Time
		gap := &jobs[front]
		if readyCount > 0 {
			checkGap = p.checkSlowGap(gap, busy, func(n *endpoint) bool { return failed[n] })
		}
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
			var worker *endpoint
			for k := range p.nodes {
				i := (p.next + k) % len(p.nodes)
				n := p.nodes[i]
				if busy[n.historyKey] || failed[n] || p.now().Before(n.retry) {
					continue
				}
				worker = n
				p.next = (i + 1) % len(p.nodes)
				break
			}
			if worker == nil {
				break
			}
			busy[worker.historyKey] = true
			p.start(fetchCtx, &wg, results, slot, worker, j, false)
		}
		if len(busy) == 0 {
			return fmt.Errorf("no node could complete %s range %d..%d", p.network, jobs[front].from, jobs[front].to)
		}
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
			j.retry = true
			j.events = nil
			failed[result.node] = true
			continue
		}
		j.done = true
		readyCount++
		j.owner = result.node
		if result.slot != front {
			j.bufferedBytes = activityBytes(j.events)
			bufferedBytes += j.bufferedBytes
		}
		for front < len(jobs) && jobs[front].done {
			ready := &jobs[front]
			readyCount--
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

// ScanWindow preserves the multi-node lookahead in units of durable work.
func (p *SourcePool) ScanWindow() int {
	active := map[string]bool{}
	for _, n := range p.nodes {
		if !p.now().Before(n.retry) {
			active[n.historyKey] = true
		}
		if len(active) == parallelism {
			break
		}
	}
	return max(2, len(active)*rangesPerLane)
}

// ScanPending serves the store's sparse work list. The legacy Scan/Read API
// still returns contiguous data for callers that do not own a gap store.
func (p *SourcePool) ScanPending(ctx context.Context, spans []chains.Span, finish func(chains.Span, chains.BlockLink, []chains.Activity, error) error) error {
	defer p.trimConnections()
	type task struct {
		segment
		tried       map[*endpoint]bool
		slowHistory string
	}
	jobs := make([]task, len(spans))
	for i, r := range spans {
		jobs[i] = task{segment: segment{from: r.First, to: r.Last}, tried: map[*endpoint]bool{}}
	}
	calls, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	busy := map[string]bool{}
	results := make(chan completion, parallelism)
	highestFinished := int64(-1)
	for remaining := len(jobs); remaining > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		var oldest *task
		for i := range jobs {
			j := &jobs[i]
			if !j.done && (oldest == nil || j.from < oldest.from) {
				oldest = j
			}
		}
		var checkGap <-chan time.Time
		if oldest != nil && highestFinished > oldest.from && len(oldest.tried) < 3 {
			checkGap = p.checkSlowGap(&oldest.segment, busy, func(n *endpoint) bool { return oldest.tried[n] || n.historyKey == oldest.slowHistory })
		}
		for i := range jobs {
			if oldest.yielding || len(busy) >= parallelism {
				break
			}
			j := &jobs[i]
			if j.running || j.done {
				continue
			}
			waiting := false
			var node *endpoint
			for k := range p.nodes {
				// Bound per-span failover so one missing block cannot exhaust
				// every discovered provider in the same round.
				if len(j.tried) >= 3 {
					break
				}
				index := (p.next + k) % len(p.nodes)
				n := p.nodes[index]
				if j.tried[n] || n.historyKey == j.slowHistory || p.now().Before(n.retry) {
					continue
				}
				if busy[n.historyKey] {
					waiting = true
					continue
				}
				node = n
				p.next = (index + 1) % len(p.nodes)
				break
			}
			if node == nil {
				if waiting {
					continue
				}
				failure := fmt.Errorf("no node could complete %s range %d..%d", p.network, j.from, j.to)
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
			}
			j.events = nil
			if err == nil {
				highestFinished = max(highestFinished, j.to)
				j.done = true
				remaining--
			} else {
				j.to = spans[result.slot].Last
			}
		}
	}
	return nil
}
