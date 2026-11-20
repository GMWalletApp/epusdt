package task

import (
	"context"
	"errors"
	"fmt"
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
	defer p.mu.Unlock()
	if p.registered[w] {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = p.syncLocked(ctx); err != nil {
		return err
	}
	if !p.registered[w] {
		return errors.New("payment address is not registered with ChainScanner")
	}
	return nil
}

// embeddedWorker adapts EPUSDT's live node selection to the same scanner
// engine used by the standalone service. Only this worker owns the source.
type embeddedWorker struct {
	name   string
	store  *scanner.Store
	src    scanner.Source
	active scanner.Network
	nodeID uint64
	seeded bool
}

func (w *embeddedWorker) close() {
	if w.src != nil {
		w.src.Close()
		w.src = nil
	}
}

func runEmbeddedNetwork(ctx context.Context, name string, store *scanner.Store) {
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
		case <-scanner.Wakeups(worker.src):
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
	var node *mdb.RpcNode
	var n scanner.Network
	for i := range nodes {
		candidate, ok := scannerNodeConfig(nodes[i], nodes)
		if ok {
			node = &nodes[i]
			n = candidate
			break
		}
	}
	if node == nil {
		w.close()
		return delay, nil
	}
	n.Confirmations = int64(chain.MinConfirmations)
	if scanner.IsEVM(w.name) {
		if scanner.NativeAsset(w.name) == "" {
			n.Lookback = 2048
		}
		if !w.seeded {
			old, err := data.GetEvmScanCursor(w.name)
			if err != nil {
				return delay, err
			}
			if old.ID != 0 {
				if err = w.store.SeedCursor(ctx, w.name, old.LastBlock); err != nil {
					return delay, err
				}
			}
			w.seeded = true
		}
	} else if w.name == mdb.NetworkAptos {
		n.Lookback = 100
	}
	if n != w.active || node.ID != w.nodeID {
		w.close()
		w.active = n
		w.nodeID = node.ID
	}
	if w.src == nil {
		w.src, err = scanner.NewSource(ctx, n)
	}
	var behind bool
	if err == nil {
		behind, err = w.store.Step(ctx, n, w.src)
	}
	if err != nil {
		w.close()
		data.RecordRpcNodeFailure(node.ID)
		data.RecordRpcFailure(w.name)
		return delay, fmt.Errorf("node %d failed, cursor retained (%T)", node.ID, err)
	}
	data.RecordRpcNodeSuccess(node.ID)
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
