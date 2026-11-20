package nodes

import (
	"context"
	"strings"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

// Run shares one cursor and one scheduler across subscribers to a network.
func (r *NodeRegistry) Run(ctx context.Context, n chains.Network) {
	r.store.WhileWatched(ctx, n.Name, func(live context.Context) {
		p := newRegistryPool(live, r, n)
		defer p.Close()
		open := func(context.Context, chains.Network) (chains.Source, error) { return p, nil }
		engine.RunWithSource(live, r.store, n, open, func(chains.Source) <-chan struct{} { return p.Wakeups() })
	})
}

type nodePool struct {
	*SourcePool
	registry *NodeRegistry
	settings chains.Network
	revision uint64
	loaded   bool
}

func newRegistryPool(ctx context.Context, r *NodeRegistry, n chains.Network) *nodePool {
	return &nodePool{SourcePool: NewSourcePool(ctx, n.Name), registry: r, settings: n}
}
func (p *nodePool) Head(ctx context.Context) (int64, error) {
	v := p.registry.revision.Load()
	if !p.loaded || v != p.revision {
		rows, err := p.registry.List(ctx, p.settings.Name)
		if err != nil {
			return 0, err
		}
		origins := make(map[chains.Network]Node, len(rows))
		var preferred, others []chains.Network
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
			} else {
				others = append(others, cfg)
			}
		}
		if err := p.SetNodes(append(preferred, others...)); err != nil {
			return 0, err
		}
		p.open = func(ctx context.Context, cfg chains.Network) (chains.Source, error) {
			return openNode(ctx, origins[cfg], cfg)
		}
		p.loaded, p.revision = true, v
	}
	return p.SourcePool.Head(ctx)
}
