package scanner

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// Run uses one source and one durable cursor for the configured network.
func (r *NodeRegistry) Run(ctx context.Context, n Network) {
	r.store.WhileWatched(ctx, n.Name, func(live context.Context) {
		pool := &nodePool{registry: r, settings: n, lifetime: live}
		r.store.run(live, n, func(context.Context, Network) (Source, error) { return pool, nil })
	})
}

type nodePool struct {
	lifetime context.Context
	registry *NodeRegistry
	settings Network
	active   Source
	id       int64
	revision uint64
}

func (p *nodePool) Head(ctx context.Context) (int64, error) {
	v := p.registry.revision.Load()
	if p.active == nil || v != p.revision {
		nodes, err := p.registry.List(ctx, p.settings.Name)
		if err != nil {
			return 0, err
		}
		nodes = slices.DeleteFunc(nodes, func(n Node) bool {
			return !n.Scannable || (strings.HasPrefix(p.settings.Protocol, "solidity-") && !strings.HasPrefix(n.Protocol, "solidity-"))
		})
		index := slices.IndexFunc(nodes, func(n Node) bool { return n.ID == p.id })
		if index < 0 {
			p.Close()
		}
		if len(nodes) == 0 {
			return 0, errors.New("no configured scan node available")
		}
		if p.active == nil {
			next := (index + 1) % len(nodes)
			if p.id == 0 {
				for i, n := range nodes {
					if _, ok := p.registry.configured[n.NodeEndpoint]; ok {
						next = i
						break
					}
				}
			}
			n := nodes[next]
			p.id = n.ID
			endpoint := p.registry.network(n, p.settings)
			live := p.lifetime
			if live == nil {
				live = ctx
			}
			p.active, err = NewSource(live, endpoint)
			if err != nil {
				return 0, errors.New("cannot open scan node")
			}
		}
		p.revision = v
	}
	h, err := p.active.Head(ctx)
	if err != nil {
		return 0, errors.New("node height read failed")
	}
	return h, nil
}
func (p *nodePool) Read(ctx context.Context, next, head int64) (int64, []Activity, error) {
	h, events, err := p.active.Read(ctx, next, head)
	if err != nil {
		return 0, nil, errors.New("node block read failed")
	}
	return h, events, nil
}
func (p *nodePool) Close() {
	if p.active != nil {
		p.active.Close()
		p.active = nil
	}
}
func (p *nodePool) wakeups() <-chan struct{} { return Wakeups(p.active) }
