package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	scanner "github.com/GMWalletApp/ChainScanner"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type config struct {
	Listen, Database string
	Networks         []scanner.Network
	Consumers        map[string]string
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
		c.Database = "chainscanner.sqlite"
	}
	seen := map[string]bool{}
	for _, n := range c.Networks {
		if seen[n.Name] || !scanner.Supported(n.Name) || n.URL == "" || n.Confirmations < 0 || n.Lookback < 0 {
			return fmt.Errorf("invalid or duplicate network %q", n.Name)
		}
		if err := scanner.ValidateNetwork(n); err != nil {
			return fmt.Errorf("%s: %w", n.Name, err)
		}
		seen[n.Name] = true
	}
	s, err := scanner.Open(c.Database)
	if err != nil {
		return err
	}
	defer s.Close()
	handler, err := scanner.Handler(s, c.Consumers)
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
	for _, n := range c.Networks {
		wg.Add(1)
		go func() { defer wg.Done(); s.Run(ctx, n) }()
	}
	srv := http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
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
