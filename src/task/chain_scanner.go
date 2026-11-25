package task

import (
	"context"
	"errors"
	"sync"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/internal/diagnostics"
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
	addresses, err := data.PaymentWatchAddresses()
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
	watches := make([]scanner.Watch, 0, len(addresses))

	seen := make(map[scanner.Watch]bool, len(addresses))
	for _, w := range addresses {
		if w.Network == mdb.NetworkXLayer || !enabled[w.Network] {
			continue
		}
		a, e := scanner.Canonical(w.Network, w.Address)
		if e != nil {
			log.Sugar.Warnf("[ChainScanner] invalid payment address on %s", w.Network)
			continue
		}
		w.Address = a
		if !seen[w] {
			watches = append(watches, w)
			seen[w] = true
		}
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

func consumeQueue(ctx context.Context, network string, feed scanner.Feed, next func() (*scanner.Delivery, error)) {
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
			diagnostics.Record(diagnostics.Event{Event: "scanner.delivery", Outcome: "retry", Network: network})
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
	diagnostics.Record(diagnostics.Event{Event: "scan.observed", Network: e.Network, Hash: e.TxID, Height: e.Height, Confirmations: e.Confirmations})
	err = service.ApplyChainActivity(ctx, e)
	if err == nil {
		data.RecordRpcBlockHeight(e.Network, e.Height)
	} else {
		diagnostics.Record(diagnostics.Event{Event: "scan.process", Outcome: "failed", Network: e.Network, Hash: e.TxID, Height: e.Height})
	}
	return err
}
