package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
)

type config struct {
	Listen, Database string
	Networks         []scanner.Network
	Consumers        map[string]string
	Blacklist        []string `json:"blacklist"`
	AdminToken       string   `json:"admin_token"`
}

func run() error {
	path := flag.String("config", "chainscanner.json", "JSON configuration path")
	flag.Parse()
	b, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	var c config
	if err = json.Unmarshal(b, &c); err != nil {
		return err
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:9081"
	}
	if c.Database == "" {
		c.Database = ":memory:"
	}
	seen := map[string]bool{}
	for _, n := range c.Networks {
		if seen[n.Name] || !scanner.Supported(n.Name) || n.Confirmations < 0 || n.Lookback < 0 {
			return fmt.Errorf("invalid or duplicate network %q", n.Name)
		}
		if n.URL != "" {
			if err := scanner.ValidateNetwork(n); err != nil {
				return fmt.Errorf("%s: %w", n.Name, err)
			}
		} else if n.APIKey != "" || n.Protocol != "" || n.HistoryURL != "" || n.HistoryAPIKey != "" {
			return fmt.Errorf("%s: endpoint options require a url", n.Name)
		}
		seen[n.Name] = true
	}
	s, err := scanner.Open(c.Database)
	if err != nil {
		return err
	}
	defer s.Close()
	registry, err := scanner.NewNodeRegistryWithBlockedHosts(context.Background(), s, c.Networks, c.Blacklist)
	if err != nil {
		return err
	}
	handler, err := registry.Handler(c.Consumers, c.AdminToken)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var wg sync.WaitGroup
	wg.Go(func() { registry.RunChainList(ctx) })
	for _, n := range c.Networks {
		wg.Go(func() { registry.Run(ctx, n) })
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := http.Server{Protocols: protocols, BaseContext: func(net.Listener) context.Context { return ctx }, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	err = srv.Serve(ln)
	stop()
	wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
