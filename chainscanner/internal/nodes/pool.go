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
	p.observe = func(cfg chains.Network, h headSample) {
		node, ok := p.inventory[cfg]
		// Paired transports obtain their scan height from the companion history API.
		// The admin height of their primary transport has different semantics.
		if !ok || chains.NeedsHistory(cfg.Name, node.Protocol) {
			return
		}
		if err := r.saveHead(ctx, node, h); err != nil && ctx.Err() == nil {
			log.Printf("chain %s: saving node health: %v", cfg.Name, err)
		}
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
			} else if n.Origin == "chainlist" {
				discovered = append(discovered, cfg)
			} else {
				owned = append(owned, cfg)
			}
		}
		// A retired and rediscovered URL can acquire a new row ID between
		// scan steps. Its new owner gets a fresh connection and penalty.
		p.nodes = slices.DeleteFunc(p.nodes, func(n *endpoint) bool {
			old, cur := p.inventory[n.cfg], origins[n.cfg]
			if old.ID != cur.ID || old.Origin != cur.Origin {
				p.retire(n)
				return true
			}
			return false
		})
		if err := p.SetNodes(slices.Concat(preferred, owned, discovered)); err != nil {
			return err
		}
		p.open = func(ctx context.Context, cfg chains.Network) (chains.Source, error) {
			return openNode(ctx, origins[cfg], cfg)
		}
		p.loaded, p.revision, p.inventory = true, v, origins
	}
	return nil
}
