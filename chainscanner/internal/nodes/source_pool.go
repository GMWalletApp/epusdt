package nodes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
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
	scanAttempts   *scanAttempts
	lanes          *providerLanes
	live           *liveQueue
	uses           uint64
	life           context.Context
	network        string
	nodes          []*endpoint
	next, headNext int
	repairNext     int
	anchorHeight   int64
	anchorHash     string
	open           func(context.Context, chains.Network) (chains.Source, error)
	now            func() time.Time
	// Report receives completed provider attempts. Reports from a Read are serialized.
	Report      func(chains.Network, bool)
	observe     func(chains.Network, headSample)
	repairHead  func(context.Context) (int64, error)    // recover the height of durable failed work
	observeRead func(chains.Network, string, time.Time) // failed read or recovery, serialized by the scheduler
}

type endpoint struct {
	weight, picks              int
	fallback                   bool // operator-funded history endpoint; failed ranges only
	lastUse                    uint64
	measured, accepted         headSample
	published                  time.Time
	publishedOK                bool
	cfg                        chains.Network
	src                        chains.Source
	stop                       context.CancelFunc
	tip                        int64
	headFailures, readFailures failureStreak
	historyKey                 string
	retry                      time.Time
	lastFailure                string
	observed                   time.Time
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
			n = &endpoint{cfg: cfg, tip: -1, historyKey: historyLane(cfg.Name, history)}
		}
		next = append(next, n)
	}
	for _, n := range next {
		delete(old, n.cfg)
	}
	for _, n := range old {
		p.retire(n)
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
	p.stopLive()
	for _, n := range p.nodes {
		p.release(n)
	}
}

// Discard a removed endpoint's watch-period health as well as its connection.
func (p *SourcePool) release(n *endpoint) {
	if p.observeRead != nil && n.readFailures != (failureStreak{}) {
		p.observeRead(n.cfg, "", time.Time{})
	}
	n.close()
}

// Discovery can provide many URLs. Retain the recently used connections
// while preserving cooldowns for every candidate in the registry.
func (p *SourcePool) trimConnections() {
	var live []*endpoint
	for _, n := range p.nodes {
		if (p.live == nil || p.live.running[n] == nil) && n.src != nil {
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
		if n.fallback || (p.live != nil && p.live.running[n] != nil) {
			continue
		}
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
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		return context.DeadlineExceeded
	}
	n.src, n.stop = s, stop
	return nil
}
func (p *SourcePool) record(n *endpoint, read bool, err error) {
	// fetch may have failed its prerequisite height check before issuing a read.
	read = read && n.measured.err == nil
	// A repair endpoint may open a stream when dialing. Close it when the
	// request ends so an idle paid subscription cannot keep consuming traffic.
	if n.fallback {
		defer n.close()
	}
	hadReadFailure := n.readFailures != (failureStreak{})
	p.acceptHead(n)
	if errors.Is(err, errNodeBehind) {
		return
	}
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
		n.lastFailure = failureReason(err)
		if cooldown := counter.fail(); cooldown > 0 {
			n.retry = p.now().Add(cooldown)
		}
		n.close()
	}
	if read && p.observeRead != nil && (err != nil || hadReadFailure) {
		reason := ""
		if err != nil {
			reason = n.lastFailure
		}
		p.observeRead(n.cfg, reason, n.retry)
	}
	if p.Report != nil && (read || err != nil || n.readFailures == (failureStreak{})) {
		p.Report(n.cfg, err == nil)
	}
}
func (p *SourcePool) height(ctx context.Context, n *endpoint) (height int64, resultErr error) {
	defer p.lanes.scan(chains.ProviderKey(n.cfg))()
	return p.readHeight(ctx, n)
}
func (p *SourcePool) readHeight(ctx context.Context, n *endpoint) (height int64, resultErr error) {
	start := p.now()
	defer func() {
		n.measured = headSample{start: start, latency: p.now().Sub(start), height: height, err: resultErr}
	}()

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
		n.observed = p.now()
	}
	return h, err
}
func (p *SourcePool) Head(ctx context.Context) (int64, error) {
	defer p.trimConnections()
	var failure error
	for k := range p.nodes {
		i := (p.headNext + k) % len(p.nodes)
		n := p.nodes[i]
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if n.fallback || p.now().Before(n.retry) {
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
		failure = n.diagnostic(err)
	}
	return 0, p.unavailable("height", failure)
}
