package task

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/util/log"
)

// Keep the pinned scanner's API (including strict verification) identical to
// standalone mode. Its listener and credentials are private to this process.
func startEmbeddedScanner(ctx context.Context, path string, networks []scanner.Network) (config.ScannerConnection, func(), error) {
	// The node registry may contain private RPC URLs.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return config.ScannerConnection{}, nil, err
	}
	if err := file.Close(); err != nil {
		return config.ScannerConnection{}, nil, err
	}
	store, err := scanner.Open(path)
	if err != nil {
		return config.ScannerConnection{}, nil, err
	}
	registry, err := scanner.NewNodeRegistry(ctx, store, networks)
	if err != nil {
		store.Close()
		return config.ScannerConnection{}, nil, err
	}
	conn := config.ScannerConnection{Embedded: true, ConsumerToken: rand.Text(), AdminToken: rand.Text(), Transport: "http"}
	handler, err := registry.Handler(map[string]string{paymentConsumer: conn.ConsumerToken}, conn.AdminToken)
	if err != nil {
		store.Close()
		return config.ScannerConnection{}, nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		store.Close()
		return config.ScannerConnection{}, nil, err
	}
	conn.URL = "http://" + listener.Addr().String()
	ctx, cancel := context.WithCancel(ctx)
	server := &http.Server{Handler: handler, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second}
	var workers sync.WaitGroup
	workers.Go(func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Sugar.Errorf("[ChainScanner] embedded server: %v", err)
		}
	})
	if len(networks) > 0 {
		workers.Go(func() { registry.RunChainList(ctx) })
	}
	for _, network := range networks {
		workers.Go(func() { registry.Run(ctx, network) })
	}
	return conn, func() {
		cancel()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := server.Shutdown(shutdown); err != nil {
			server.Close()
		}
		workers.Wait()
		store.Close()
	}, nil
}
