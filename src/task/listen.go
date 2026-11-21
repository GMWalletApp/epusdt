package task

import (
	"context"
	"errors"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
)

func Start() {
	// Hosted OKX payments settle independently of the on-chain scanner.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			XLayerPollJob{}.Run()
		}
	}()

	url, err := config.ScannerEndpoint()
	token, tokenErr := config.ScannerToken(false)
	transport := config.ScannerTransport()
	if err != nil || tokenErr != nil || (transport != "" && transport != "ws" && transport != "grpc" && transport != "http") {
		err := errors.New("configure CHAINSCANNER_URL, CHAINSCANNER_TOKEN and a supported event transport")
		service.EnsurePaymentWatch = func(string, string) error { return err }
		log.Sugar.Errorf("[ChainScanner] %v", err)
		return
	}
	ctx := context.Background()
	client := scanner.Client{URL: url, Token: token}
	verifier := newStrictClient(client)
	service.StrictTransaction = verifier.full
	subscriptions := &paymentSubscriptions{feed: client}
	service.EnsurePaymentWatch = subscriptions.ensure
	for range 4 {
		go service.RunPaymentConfirmations(ctx, client, verifier.round)
	}
	for _, network := range scannerNetworks {
		go runScannerFeed(ctx, client, network, transport)
	}
	go func() {
		for {
			if err := subscriptions.refresh(ctx); err != nil {
				log.Sugar.Errorf("[ChainScanner] subscribe: %v", err)
			}
			time.Sleep(5 * time.Second)
		}
	}()
}
