package nodes

import (
	"context"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

// Run shares one cursor and one scheduler across subscribers to a network.
func (r *NodeRegistry) Run(ctx context.Context, n chains.Network) {
	r.store.WhileWatched(ctx, n.Name, func(live context.Context) {
		p := newRegistryPool(live, r, n)
		defer p.Close()
		saveNode := p.observe
		interval := time.Duration(n.IntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 5 * time.Second
		}
		p.observe = func(cfg chains.Network, sample headSample) {
			if node, ok := p.inventory[cfg]; ok && node.Origin != "fallback" && sample.err == nil {
				head := p.bestRecentTip(sample.height, interval)
				r.store.RecordScanHead(live, n.Name, head-max(n.Confirmations-1, 0), sample.start)
			}
			saveNode(cfg, sample)
		}
		open := func(context.Context, chains.Network) (chains.Source, error) { return borrowedPool{p}, nil }
		engine.RunWithSource(live, r.store, n, open, func(chains.Source) <-chan struct{} { return p.Wakeups() })
	})
}

type nodePool struct {
	*SourcePool
	registry  *NodeRegistry
	settings  chains.Network
	revision  uint64
	loaded    bool
	inventory map[chains.Network]Node
}

func newRegistryPool(ctx context.Context, r *NodeRegistry, n chains.Network) *nodePool {
	p := &nodePool{SourcePool: NewSourcePool(ctx, n.Name), registry: r, settings: n}
	p.lanes = &r.lookup.lanes
	p.observe = func(cfg chains.Network, h headSample) {
		node, ok := p.inventory[cfg]
		// Paired transports obtain their scan height from the companion history API.
		// The admin height of their primary transport has different semantics.
		if !ok || cfg.UsesHistory(node.Protocol) {
			return
		}
		if err := r.saveHead(ctx, node, h); err != nil && ctx.Err() == nil {
			log.Printf("chain %s: saving node health: %v", cfg.Name, err)
		}
	}
	p.observeRead = func(cfg chains.Network, reason string, retry time.Time) {
		node, ok := p.inventory[cfg]
		if !ok {
			return
		}
		if reason == "" {
			r.readFailures.Delete(node.version())
			return
		}
		r.readFailures.Store(node.version(), readFailure{reason: reason, retry: retry})
	}
	return p
}

// The watch period owns the pool. The engine can release its handle after a
// failed step without disconnecting other providers that are still healthy.
type borrowedPool struct{ *nodePool }

func (borrowedPool) Close() {}

func (p *nodePool) LiveWindow(ctx, period context.Context, interval time.Duration) (int64, []int64, int, error) {
	if err := p.refresh(ctx); err != nil {
		return 0, nil, 0, err
	}
	return p.SourcePool.LiveWindow(ctx, period, interval)
}

func (p *nodePool) Head(ctx context.Context) (int64, error) {
	if err := p.refresh(ctx); err != nil {
		return 0, err
	}
	return p.SourcePool.Head(ctx)
}
func (p *nodePool) refresh(ctx context.Context) error {
	v := p.registry.revision.Load()
	if !p.loaded || v != p.revision {
		rows, err := p.registry.List(ctx, p.settings.Name)
		if err != nil {
			return err
		}
		origins := make(map[chains.Network]Node, len(rows))
		var preferred, owned, discovered []chains.Network
		for _, n := range rows {
			if !n.Scannable || (strings.HasPrefix(p.settings.Protocol, "solidity-") && !strings.HasPrefix(n.Protocol, "solidity-")) {
				continue
			}
			cfg := p.registry.network(n, p.settings)
			if chains.ValidateNetwork(cfg) != nil {
				continue
			}
			origins[cfg] = n
			if _, ok := p.registry.configured[n.NodeEndpoint]; ok {
				preferred = append(preferred, cfg)
			} else if isDiscoveryOrigin(n.Origin) {
				discovered = append(discovered, cfg)
			} else {
				owned = append(owned, cfg)
			}
		}
		// A retired and rediscovered URL can acquire a new row ID between
		// scan steps. Its new owner gets a fresh connection and penalty.
		p.nodes = slices.DeleteFunc(p.nodes, func(n *endpoint) bool {
			old, cur := p.inventory[n.cfg], origins[n.cfg]
			if old.version() != cur.version() {
				p.retire(n)
				return true
			}
			return false
		})
		if err := p.SetNodes(slices.Concat(preferred, owned, discovered)); err != nil {
			return err
		}
		// A temporary pool used to resolve a subscription boundary shares the
		// active scanner's read cooldown for this node.
		for _, n := range p.nodes {
			row := origins[n.cfg]
			n.fallback = row.Origin == "fallback"
			if row.ReadRetryAt > 0 && time.UnixMilli(row.ReadRetryAt).After(n.retry) {
				n.retry = time.UnixMilli(row.ReadRetryAt)
			}
		}
		p.repairHead = nil
		if slices.ContainsFunc(p.nodes, func(n *endpoint) bool { return n.fallback }) {
			p.repairHead = func(ctx context.Context) (int64, error) {
				last, err := p.registry.store.LastFailedHeight(ctx, p.network)
				if err != nil || last < 0 {
					return last, err
				}
				return last + max(0, p.settings.Confirmations-1), nil
			}
		}
		p.open = func(ctx context.Context, cfg chains.Network) (chains.Source, error) {
			return openNode(ctx, origins[cfg], cfg)
		}
		p.loaded, p.revision, p.inventory = true, v, origins
	}
	return nil
}
