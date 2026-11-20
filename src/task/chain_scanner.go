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

	for _, w := range wallets {
		if !enabled[w.Network] {
			continue
		}
		a, e := scanner.Canonical(w.Network, w.Address)
		if e != nil {
			log.Sugar.Warnf("[ChainScanner] invalid wallet row %d on %s", w.ID, w.Network)
			continue
		}
		watches = append(watches, scanner.Watch{Network: w.Network, Address: a})
	}
	receipt, err := p.feed.Subscribe(ctx, paymentConsumer, watches)
	if err != nil {
		return err
	}
	p.registered = make(map[scanner.Watch]bool, len(receipt.Registrations))
	for _, r := range receipt.Registrations {
		p.registered[r.Watch] = true
	}
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
	return nil
}

// embeddedWorker adapts EPUSDT's live node selection to the same scanner
// engine used by the standalone service. Only this worker owns the source.
type embeddedWorker struct {
	config      scanner.Network
	nextRefresh time.Time
	delay       time.Duration
	name        string
	store       *scanner.Store
	src         *scanner.SourcePool
	idsMu       sync.RWMutex
	ids         map[scanner.Network]uint64
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
func (w *embeddedWorker) refreshNodes(ctx context.Context, allowDisabled bool) (time.Duration, error) {
	delay := 5 * time.Second
	chain, err := data.GetChainByNetwork(w.name)
	if err != nil {
		return delay, err
	}
	if chain.ID == 0 || (!chain.Enabled && !allowDisabled) {
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
	ids := map[scanner.Network]uint64{}
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
		candidate.IntervalSeconds = int(delay / time.Second)
		configs = append(configs, candidate)
		ids[candidate] = node.ID
	}
	if len(configs) == 0 {
		w.close()
		return delay, nil
	}
	w.idsMu.Lock()
	w.ids = ids
	w.idsMu.Unlock()
	if w.src == nil {
		w.src = scanner.NewSourcePool(ctx, w.name)
		w.src.Report = func(n scanner.Network, success bool) {
			w.idsMu.RLock()
			id, ok := w.ids[n]
			w.idsMu.RUnlock()
			if !ok {
				return
			}
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
	w.config = configs[0]
	return delay, nil
}

// Take a fresh head for cold registration and the inclusive unsubscribe boundary.
// A disabled payment chain may still owe a final confirmed range.
func configureEmbedded(store *scanner.Store) {
	networks := make(map[string]scanner.Network, len(scannerNetworks))
	for _, n := range scannerNetworks {
		networks[n] = scanner.Network{Name: n}
	}
	scanner.ConfigureNetworks(store, networks, func(ctx context.Context, name string) (int64, error) {
		w := embeddedWorker{name: name, store: store}
		defer w.close()
		if _, err := w.refreshNodes(ctx, true); err != nil {
			return 0, err
		}
		if w.src == nil {
			return 0, scanner.ErrCursorNotReady
		}
		return w.src.Head(ctx)
	})
}

func (w *embeddedWorker) step(ctx context.Context) (time.Duration, error) {
	if !time.Now().Before(w.nextRefresh) {
		delay, err := w.refreshNodes(ctx, w.store.Draining(w.name))
		if err != nil {
			return delay, err
		}
		w.delay = delay
		w.nextRefresh = time.Now().Add(min(delay, 5*time.Second))
	}
	delay := w.delay
	if w.src == nil {
		return delay, nil
	}
	behind, err := w.store.Pump(ctx, w.config, w.src)
	if err != nil {
		if ctx.Err() != nil {
			return delay, nil
		}
		data.RecordRpcFailure(w.name)
		return delay, fmt.Errorf("%s scanner: %w", w.name, err)
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
	var wg sync.WaitGroup
	for _, network := range scannerNetworks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			consumeQueue(ctx, feed, func() (*scanner.Delivery, error) {
				return feed.PendingNetwork(ctx, paymentConsumer, network, 15*time.Second)
			})
		}()
	}
	wg.Wait()
}
func consumeQueue(ctx context.Context, feed scanner.Feed, next func() (*scanner.Delivery, error)) {
	for ctx.Err() == nil {
		d, err := next()
		if err == nil && d != nil {
			err = processScannerActivity(ctx, d.Activity)
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

func processScannerActivity(ctx context.Context, e scanner.Activity) error {
	chain, err := data.GetChainByNetwork(e.Network)
	if err != nil {
		return err
	}
	if chain.ID == 0 || !chain.Enabled {
		return nil
	}
	return service.ApplyChainActivity(ctx, e)
}
