package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

const chainListURL = "https://chainlist.org/rpcs.json"

// RunChainList refreshes the node inventory independently of the scan workers.
func (r *NodeRegistry) RunChainList(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for ctx.Err() == nil {
		round, cancel := context.WithTimeout(ctx, 15*time.Minute)
		count, err := r.refreshChainList(round, client, chainListURL, chains.ProbePublicRPC)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("ChainList refresh failed: %v", err)
		} else {
			log.Printf("ChainList refresh: %d healthy discovered RPCs", count)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func fetchChainList(ctx context.Context, client *http.Client, address string) ([]NodeEndpoint, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog HTTP %d", res.StatusCode)
	}
	const maxSize = 16 << 20
	b, err := io.ReadAll(io.LimitReader(res.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSize {
		return nil, errors.New("catalog exceeds size limit")
	}
	var catalog []struct {
		ID  json.Number `json:"chainId"`
		RPC []struct {
			URL string `json:"url"`
		} `json:"rpc"`
	}
	if err = json.Unmarshal(b, &catalog); err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for network := range chains.EVMNetworks() {
		names[chains.EVMChainID(network)] = network
	}
	seen := map[NodeEndpoint]bool{}
	var endpoints []NodeEndpoint
	for _, chain := range catalog {
		id, err := chain.ID.Int64()
		name, ok := names[id]
		if err != nil || !ok {
			continue
		}
		for _, entry := range chain.RPC {
			e := NodeEndpoint{name, chains.PublicRPCProtocol(entry.URL), entry.URL}
			if e.Protocol != "" && !seen[e] {
				endpoints = append(endpoints, e)
				seen[e] = true
			}
		}
	}
	if len(endpoints) == 0 {
		return nil, errors.New("catalog contains no supported EVM endpoints")
	}
	return endpoints, nil
}

type observation struct {
	endpoint NodeEndpoint
	height   int64
	at       time.Time
	latency  time.Duration
}

func (r *NodeRegistry) refreshChainList(ctx context.Context, client *http.Client, address string, probe func(context.Context, chains.Network) (int64, error)) (int, error) {
	candidates, err := fetchChainList(ctx, client, address)
	if err != nil {
		return 0, err
	}
	rows, err := r.db.QueryContext(ctx, "SELECT network,protocol,url FROM rpc_nodes WHERE removed=1 OR origin!='chainlist'")
	if err != nil {
		return 0, err
	}
	excluded := map[NodeEndpoint]bool{}
	for rows.Next() {
		var e NodeEndpoint
		if err = rows.Scan(&e.Network, &e.Protocol, &e.URL); err != nil {
			rows.Close()
			return 0, err
		}
		if e.Protocol == "jsonrpc" {
			e.Protocol = "http"
		}
		excluded[e] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	// Each worker owns its result slot. No database locks are held during I/O.
	results := make([]observation, len(candidates))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for i := range jobs {
				e := candidates[i]
				start := time.Now()
				attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
				h, err := probe(attempt, chains.Network{Name: e.Network, Protocol: e.Protocol, URL: e.URL})
				cancel()
				if err == nil && h > 0 {
					results[i] = observation{e, h, start, time.Since(start)}
				}
			}
		})
	}
	for i, e := range candidates {
		if excluded[e] || ctx.Err() != nil {
			continue
		}
		select {
		case jobs <- i:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return r.applyChainList(ctx, results)
}

func (r *NodeRegistry) applyChainList(ctx context.Context, results []observation) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	keep := map[NodeEndpoint]bool{}
	for _, obs := range results {
		if obs.height == 0 {
			continue
		}
		e := obs.endpoint
		_, err = tx.ExecContext(ctx, `INSERT INTO rpc_nodes(network,protocol,url,origin,status,height,checked_at,latency)
        VALUES(?,?,?,'chainlist','ok',?,?,?) ON CONFLICT(network,protocol,url) DO UPDATE SET
        status='ok',height=excluded.height,checked_at=excluded.checked_at,latency=excluded.latency
        WHERE origin='chainlist' AND removed=0 AND (checked_at IS NULL OR checked_at<=excluded.checked_at)`, e.Network, e.Protocol, e.URL, obs.height, obs.at.UnixMilli(), obs.latency.Milliseconds())
		if err != nil {
			return 0, err
		}
		keep[e] = true
	}
	// Physically retire only discovery-owned rows. Operator deletions remain
	// tombstones, so a future catalog cannot resurrect an explicitly removed URL.
	rows, err := tx.QueryContext(ctx, "SELECT id,network,protocol,url FROM rpc_nodes WHERE origin='chainlist' AND removed=0")
	if err != nil {
		return 0, err
	}
	var expired []int64
	count := 0
	for rows.Next() {
		var id int64
		var e NodeEndpoint
		if err = rows.Scan(&id, &e.Network, &e.Protocol, &e.URL); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[e] {
			expired = append(expired, id)
		} else {
			count++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range expired {
		if _, err = tx.ExecContext(ctx, "DELETE FROM rpc_nodes WHERE id=?", id); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	r.revision.Add(1)
	return count, nil
}
