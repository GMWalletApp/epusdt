package task

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
)

func Start() (config.ScannerConnection, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	var closeEmbedded func()
	stop := func() {
		cancel()
		workers.Wait()
		if closeEmbedded != nil {
			closeEmbedded()
		}
	}
	// Hosted OKX payments settle independently of the on-chain scanner.
	workers.Go(func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				XLayerPollJob{}.Run()
			}
		}
	})
	connection := config.ExternalScanner()
	var err error
	if connection.URL == "" {
		connection, closeEmbedded, err = startLocalScanner(ctx)
	}
	data.UseScannerNodes(connection)
	if err == nil && connection.Embedded {
		if _, migrationErr := data.MigrateScannerNodes(ctx, connection, true, nil); migrationErr != nil {
			log.Sugar.Errorf("[ChainScanner] node migration: %v", migrationErr)
		}
	}
	if err == nil {
		err = startPaymentFeed(ctx, &workers, connection)
	}
	if err != nil {
		service.EnsurePaymentWatch = func(string, string) error { return err }
		log.Sugar.Errorf("[ChainScanner] %v", err)
	}
	return connection, stop
}

func startLocalScanner(ctx context.Context) (config.ScannerConnection, func(), error) {
	chains, err := data.ListChains()
	if err != nil {
		return config.ScannerConnection{}, nil, err
	}
	var networks []scanner.Network
	// Subscription reconciliation already observes changes to chain enablement.
	// Register disabled networks too so enabling them later needs no restart.
	for _, chain := range chains {
		if scanner.Supported(chain.Network) {
			networks = append(networks, scanner.Network{Name: chain.Network, Confirmations: int64(chain.MinConfirmations), IntervalSeconds: chain.ScanIntervalSec})
		}
	}
	return startEmbeddedScanner(ctx, filepath.Join(config.RuntimePath, "chainscanner.sqlite"), networks)
}

func startPaymentFeed(ctx context.Context, workers *sync.WaitGroup, connection config.ScannerConnection) error {
	url, err := connection.Endpoint()
	if err != nil {
		return err
	}
	token, err := connection.Token(false)
	if err != nil {
		return err
	}
	transport := connection.Transport
	if transport != "" && transport != "ws" && transport != "grpc" && transport != "http" {
		return errors.New("unsupported CHAINSCANNER_TRANSPORT")
	}
	client := scanner.Client{URL: url, Token: token}
	verifier := newStrictClient(client)
	service.StrictTransaction = verifier.full
	subscriptions := &paymentSubscriptions{feed: client}
	service.EnsurePaymentWatch = subscriptions.ensure
	for range 4 {
		workers.Go(func() { service.RunPaymentConfirmations(ctx, client, verifier.automatic) })
	}
	for _, network := range scannerNetworks {
		workers.Go(func() { runScannerFeed(ctx, client, network, transport) })
	}
	workers.Go(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			if err := subscriptions.refresh(ctx); err != nil && ctx.Err() == nil {
				log.Sugar.Errorf("[ChainScanner] subscribe: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	return nil
}
