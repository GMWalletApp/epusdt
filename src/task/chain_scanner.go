package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
)

const paymentConsumer = "epusdt"

var scannerNetworks = []string{mdb.NetworkEthereum, mdb.NetworkBsc, mdb.NetworkPolygon, mdb.NetworkPlasma, mdb.NetworkTron, mdb.NetworkTon, mdb.NetworkAptos, mdb.NetworkSolana, mdb.NetworkSui, mdb.NetworkArbitrum, mdb.NetworkHyperEVM, mdb.NetworkMonad, mdb.NetworkArc, mdb.NetworkAvalanche, mdb.NetworkOptimism, mdb.NetworkBase}

type paymentSubscriptions struct {
	mu         sync.Mutex
	feed       scanner.Feed
	registered map[scanner.Watch]bool
}

func (p *paymentSubscriptions) refresh(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.syncLocked(ctx)
}

func (p *paymentSubscriptions) syncLocked(ctx context.Context) error {
	wallets, err := data.GetAvailableWalletAddress()
	if err != nil {
		return err
	}
	chains, err := data.ListEnabledChains()
	if err != nil {
		return err
	}
	enabled := map[string]bool{}
	for _, c := range chains {
		enabled[c.Network] = true
	}
	watches := make([]scanner.Watch, 0, len(wallets))
	registered := map[scanner.Watch]bool{}
	for _, w := range wallets {
		if !enabled[w.Network] {
			continue
		}
		a, e := scanner.Canonical(w.Network, w.Address)
		if e != nil {
			log.Sugar.Warnf("[ChainScanner] invalid wallet row %d on %s", w.ID, w.Network)
			continue
		}
		v := scanner.Watch{Network: w.Network, Address: a}
		registered[v] = true
		watches = append(watches, v)
	}
	if err = p.feed.Subscribe(ctx, paymentConsumer, watches); err != nil {
		return err
	}
	p.registered = registered
	return nil
}

func (p *paymentSubscriptions) ensure(network, address string) error {
	a, err := scanner.Canonical(network, address)
	if err != nil {
		return err
	}
	w := scanner.Watch{Network: network, Address: a}
	p.mu.Lock()
	_, remote := p.feed.(scanner.Client)
	if !p.registered[w] || remote {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = p.syncLocked(ctx)
		cancel()
	}
	registered := p.registered[w]
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if !registered {
		return errors.New("payment address is not registered with ChainScanner")
	}
	return p.waitForCursor(network)
}

// Orders can expose a payment address as soon as registration returns.
// The worker for this exact network must first have observed its ledger boundary.
func (p *paymentSubscriptions) waitForCursor(network string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		h, e := p.feed.Height(ctx, network)
		if e != nil {
			return e
		}
		if h >= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s scanner cursor unavailable: %w", network, ctx.Err())
		case <-tick.C:
		}
	}
}

// embeddedWorker adapts EPUSDT's live node selection to the same scanner
// engine used by the standalone service. Only this worker owns the source.
type embeddedWorker struct {
	name  string
	store *scanner.Store
	src   *scanner.SourcePool
	ids   map[scanner.Network]uint64
}

func (w *embeddedWorker) close() {
	if w.src != nil {
		w.src.Close()
		w.src = nil
	}
}

func (w *embeddedWorker) wakeups() <-chan struct{} {
	if w.src == nil {
		return nil
	}
	return w.src.Wakeups()
}

func runEmbeddedNetwork(ctx context.Context, name string, store *scanner.Store) {
	store.WhileWatched(ctx, name, func(live context.Context) { runActivePaymentNetwork(live, name, store) })
}
func runActivePaymentNetwork(ctx context.Context, name string, store *scanner.Store) {
	worker := embeddedWorker{name: name, store: store}
	defer worker.close()
	for ctx.Err() == nil {
		delay, err := worker.step(ctx)
		if err != nil {
			log.Sugar.Warnf("[ChainScanner] %s: %v", name, err)
		}
		if delay <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-worker.wakeups():
		case <-time.After(delay):
		}
	}
}

func scannerNodeConfig(node mdb.RpcNode, candidates []mdb.RpcNode) (scanner.Network, bool) {
	n := scanner.Network{Name: node.Network, Protocol: node.Type, URL: node.Url, APIKey: node.ApiKey}
	if scanner.NeedsHistory(n.Name, n.Protocol) {
		for _, other := range candidates {
			if other.ID == node.ID || other.Network != node.Network {
				continue
			}
			if other.Type == mdb.RpcNodeTypeHttp || other.Type == "rest" || (n.Name == "solana" && other.Type == "jsonrpc") {
				n.HistoryURL = other.Url
				n.HistoryAPIKey = other.ApiKey
				break
			}
		}
	}
	return n, scanner.ValidateNetwork(n) == nil
}
func (w *embeddedWorker) step(ctx context.Context) (time.Duration, error) {
	delay := 5 * time.Second
	chain, err := data.GetChainByNetwork(w.name)
	if err != nil {
		return delay, err
	}
	if chain.ID == 0 || !chain.Enabled {
		w.close()
		return delay, nil
	}
	if chain.ScanIntervalSec > 0 {
		delay = time.Duration(chain.ScanIntervalSec) * time.Second
	}
	nodes, err := data.GeneralScannerNodes(w.name, scanner.Protocols(w.name))
	if err != nil {
		return delay, err
	}
	var configs []scanner.Network
	w.ids = map[scanner.Network]uint64{}
	for _, node := range nodes {
		if node.Network != w.name {
			continue
		}
		candidate, ok := scannerNodeConfig(node, nodes)
		if !ok {
			continue
		}
		if len(configs) > 0 && strings.HasPrefix(configs[0].Protocol, "solidity-") && !strings.HasPrefix(candidate.Protocol, "solidity-") {
			continue
		}
		candidate.Confirmations = int64(chain.MinConfirmations)
		configs = append(configs, candidate)
		w.ids[candidate] = node.ID
	}
	if len(configs) == 0 {
		w.close()
		return delay, nil
	}
	if w.src == nil {
		w.src = scanner.NewSourcePool(ctx, w.name)
		w.src.Report = func(n scanner.Network, success bool) {
			id := w.ids[n]
			if success {
				data.RecordRpcNodeSuccess(id)
			} else {
				data.RecordRpcNodeFailure(id)
			}
		}
	}
	if err = w.src.SetNodes(configs); err != nil {
		return delay, err
	}
	behind, err := w.store.Step(ctx, configs[0], w.src)
	if err != nil {
		if ctx.Err() != nil {
			return delay, nil
		}
		data.RecordRpcFailure(w.name)
		return delay, fmt.Errorf("%s scan stopped at the last committed range: %w", w.name, err)
	}
	data.RecordRpcSuccess(w.name)
	if h, err := w.store.Height(ctx, w.name); err == nil && h >= 0 {
		data.RecordRpcBlockHeight(w.name, h)
	}
	if behind {
		return 0, nil
	}
	return delay, nil
}

func consumeScanner(ctx context.Context, feed scanner.Feed) {
	lanes, ok := feed.(interface {
		PendingNetwork(context.Context, string, string, time.Duration) (*scanner.Delivery, error)
	})
	if ok {
		// Probe without acknowledging. Older standalone services expose the global queue.
		for ctx.Err() == nil {
			_, err := lanes.PendingNetwork(ctx, paymentConsumer, scannerNetworks[0], 0)
			if errors.Is(err, scanner.ErrAPINotFound) {
				break
			}
			if err == nil {
				var wg sync.WaitGroup
				for _, network := range scannerNetworks {
					wg.Add(1)
					go func() {
						defer wg.Done()
						consumeQueue(ctx, feed, func() (*scanner.Delivery, error) {
							return lanes.PendingNetwork(ctx, paymentConsumer, network, 15*time.Second)
						})
					}()
				}
				wg.Wait()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
	consumeQueue(ctx, feed, func() (*scanner.Delivery, error) { return feed.Pending(ctx, paymentConsumer) })
}
func consumeQueue(ctx context.Context, feed scanner.Feed, next func() (*scanner.Delivery, error)) {
	for ctx.Err() == nil {
		d, err := next()
		if err == nil && d != nil {
			err = processConfirmedActivity(ctx, feed, d.Activity)
			if err == nil {
				err = feed.Ack(ctx, paymentConsumer, d.ID)
				if err == nil {
					continue
				}
			}
		}
		if err != nil {
			log.Sugar.Warnf("[ChainScanner] delivery pending: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func processConfirmedActivity(ctx context.Context, feed scanner.Feed, e scanner.Activity) error {
	chain, err := data.GetChainByNetwork(e.Network)
	if err != nil {
		return err
	}
	if chain.ID == 0 || !chain.Enabled {
		return nil
	}
	if e.Confirmations < int64(chain.MinConfirmations) {
		h, err := feed.Height(ctx, e.Network)
		if err != nil {
			return err
		}
		if h-e.Height+1 < int64(chain.MinConfirmations) {
			return fmt.Errorf("%s waiting for confirmations at %d", e.Network, e.Height)
		}
	}
	return service.ApplyChainActivity(ctx, e)
}
