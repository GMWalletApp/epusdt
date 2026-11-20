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
	parallelism   = 4
	reorderRanges = 128
	reorderBytes  = 32 << 20
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
	headFailures, readFailures int
	retry                      time.Time
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
	seen := map[chains.Network]bool{}
	var next []*endpoint
	for _, cfg := range configs {
		if cfg.Name != p.network || !chains.Supported(cfg.Name) {
			return errors.New("scan node must specify the pool's concrete network")
		}
		if err := chains.ValidateNetwork(cfg); err != nil {
			return err
		}
		if seen[cfg] {
			continue
		}
		seen[cfg] = true
		n := old[cfg]
		if n == nil {
			n = &endpoint{cfg: cfg, tip: -1}
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
		*counter = 0
		if read {
			n.headFailures = 0
		}
		if n.headFailures == 0 && n.readFailures == 0 {
			n.retry = time.Time{}
		}
	} else {
		*counter++
		if *counter >= 3 || !n.retry.IsZero() {
			n.retry = p.now().Add(time.Minute)
		}
		n.close()
	}
	if p.Report != nil && (read || err != nil || n.readFailures == 0) {
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
	from, to      int64
	running, done bool
	events        []chains.Activity
	first, last   chains.BlockLink
	owner         *endpoint
	bufferedBytes int64
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

type linkedReader interface {
	ReadLinkedBlock(context.Context, int64, *chains.BlockLink) (int64, []chains.Activity, error)
}

func (p *SourcePool) fetch(ctx context.Context, n *endpoint, j *segment) error {
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
		var end int64
		var rows []chains.Activity
		var err error
		if reader, ok := n.src.(linkedReader); ok && chains.IsEVM(p.network) {
			var link chains.BlockLink
			end, rows, err = reader.ReadLinkedBlock(call, start, &link)
			if err == nil && (link.Hash == "" || (start > j.from && j.last.Hash != link.Parent)) {
				return errors.New("inconsistent EVM block ancestry")
			}
			if start == j.from {
				j.first = link
			}
			j.last = link
		} else {
			end, rows, err = n.src.Read(call, start, j.to)
		}
		if err != nil {
			return err
		}
		if end < start || end > j.to {
			return errors.New("invalid node range")
		}
		for _, ev := range rows {
			if ev.Height < start || ev.Height > end || (ev.Network != "" && ev.Network != p.network) {
				return errors.New("event outside assigned network or range")
			}
		}
		j.events = append(j.events, rows...)
		start = end + 1
	}
	return nil
}
func (n *endpoint) history() string {
	if n.cfg.HistoryURL != "" {
		return n.cfg.HistoryURL
	}
	return n.cfg.URL
}

type completion struct {
	slot int
	node *endpoint
	err  error
}

// Scan dispatches more work after each completion. A slow gap can hold up
// delivery, but other endpoints keep fetching up to the reorder buffer limit.
// emit runs serially on complete, consecutive ranges while later RPCs run.
func (p *SourcePool) Scan(ctx context.Context, next, head int64, emit func(int64, []chains.Activity) error) error {
	return p.scan(ctx, next, head, 2*reorderRanges, emit)
}

func (p *SourcePool) scan(ctx context.Context, next, head int64, maxRanges int, emit func(int64, []chains.Activity) error) error {
	defer p.trimConnections()
	if next < 0 || next > head {
		return errors.New("invalid scan window")
	}
	distinct := map[string]bool{}
	for _, n := range p.nodes {
		if !p.now().Before(n.retry) {
			distinct[n.history()] = true
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
	width = min(width, max(1, (head-next+1)/int64(lanes)))
	// Periodically return to the caller for updated configuration and tip.
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
	for front := 0; front < len(jobs); {
		if err := ctx.Err(); err != nil {
			return err
		}
		// First unassigned slot has priority. A failed slot is immediately
		// eligible again, on any peer that has not failed this scan.
		for slot := front; slot < min(len(jobs), front+reorderRanges) && len(busy) < lanes; slot++ {
			// Even when full, allow the oldest missing range to retry:
			// it is the only read that can release buffered results.
			if slot != front && bufferedBytes >= reorderBytes {
				break
			}
			j := &jobs[slot]
			if j.running || j.done {
				continue
			}
			var worker *endpoint
			for k := range p.nodes {
				i := (p.next + k) % len(p.nodes)
				n := p.nodes[i]
				if busy[n.history()] || failed[n] || p.now().Before(n.retry) {
					continue
				}
				worker = n
				p.next = (i + 1) % len(p.nodes)
				break
			}
			if worker == nil {
				break
			}
			busy[worker.history()] = true
			j.running = true
			wg.Add(1)
			go func() { defer wg.Done(); results <- completion{slot, worker, p.fetch(fetchCtx, worker, j)} }()
		}
		if len(busy) == 0 {
			return fmt.Errorf("no node could complete %s range %d..%d", p.network, jobs[front].from, jobs[front].to)
		}
		var result completion
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result = <-results:
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		j := &jobs[result.slot]
		delete(busy, result.node.history())
		j.running = false
		p.record(result.node, true, result.err)
		if result.err != nil {
			j.events = nil
			failed[result.node] = true
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
