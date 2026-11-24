package nodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

const chainListURL = "https://chainlist.org/rpcs.json"

func isDiscoveryOrigin(origin string) bool {
	return origin == "chainlist" || origin == "openchainbench" || origin == "comparenodes"
}

// RunChainList refreshes the node inventory independently of the scan workers.
func (r *NodeRegistry) RunChainList(ctx context.Context) {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var wg sync.WaitGroup
	wg.Go(func() { runChainListSchedule(ctx, func(ctx context.Context) { r.refreshOpenChainBench(ctx, client) }) })
	wg.Go(func() { runChainListSchedule(ctx, func(ctx context.Context) { r.refreshCompareNodes(ctx, client) }) })
	defer wg.Wait()
	runChainListSchedule(ctx, func(ctx context.Context) {
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
	})
}

func runChainListSchedule(ctx context.Context, refresh func(context.Context)) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		refresh(ctx)
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
			protocol := chains.PublicRPCProtocol(entry.URL)
			if protocol == "" {
				continue
			}
			e := (NodeEndpoint{name, protocol, entry.URL}).identity()
			if !seen[e] {
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
	return r.refreshCandidates(ctx, "chainlist", "", candidates, probe)
}

func (r *NodeRegistry) refreshCandidates(ctx context.Context, origin, network string, candidates []NodeEndpoint, probe func(context.Context, chains.Network) (int64, error)) (int, error) {
	excluded, existing, err := discoveryInventory(ctx, r.db, origin)
	if err != nil {
		return 0, err
	}
	// Bound work on slow directories while preserving completed admissions.
	probeCtx := ctx
	if origin == "comparenodes" {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		// An unreachable prefix must not starve later candidates every round.
		rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	}
	// Each worker owns its result slot. No database locks are held during I/O.
	results := make([]observation, len(candidates))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for i := range jobs {
				e := candidates[i]
				if r.blacklist.hosts().matches(e.URL) {
					continue
				}
				start := time.Now()
				timeout := 10 * time.Second
				if origin == "openchainbench" || origin == "comparenodes" {
					timeout = 35 * time.Second
				}
				attempt, cancel := context.WithTimeout(probeCtx, timeout)
				cfg := chains.Network{Name: e.Network, Protocol: e.Protocol, URL: e.URL}
				release := r.lookup.lanes.scan(chains.ProviderKey(cfg))
				h, err := probe(attempt, cfg)
				release()
				cancel()
				if err == nil && h > 0 {
					results[i] = observation{e, h, start, time.Since(start)}
				}
			}
		})
	}
	for i, e := range candidates {
		if excluded[e.identity()] || r.blacklist.hosts().matches(e.URL) {
			continue
		}
		// Once admitted, the regular health worker owns checks and cooldowns.
		if _, known := existing[e]; known && origin == "comparenodes" {
			results[i].endpoint = e
			continue
		}
		if probeCtx.Err() != nil {
			continue
		}
		select {
		case jobs <- i:
		case <-probeCtx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return r.applyCatalog(ctx, origin, network, results)
}

// Protected identities include all built-ins, private entries and tombstones.
// Match against both snapshots: an operator may add or delete an endpoint
// while discovery is waiting on network probes.
func discoveryInventory(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, originName string) (map[NodeEndpoint]bool, map[NodeEndpoint]NodeEndpoint, error) {
	rows, err := db.QueryContext(ctx, "SELECT network,protocol,url,origin,(removed OR managed OR NOT enabled) FROM rpc_nodes ORDER BY id")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	protected := maps.Clone(retiredEndpoints)
	existing := map[NodeEndpoint]NodeEndpoint{}
	for rows.Next() {
		var e NodeEndpoint
		var origin string
		var removed bool
		if err := rows.Scan(&e.Network, &e.Protocol, &e.URL, &origin, &removed); err != nil {
			return nil, nil, err
		}
		key := e.identity()
		if removed || origin != originName {
			protected[key] = true
			continue
		}
		if _, ok := existing[key]; !ok {
			existing[key] = e
		}
	}
	return protected, existing, rows.Err()
}

func (r *NodeRegistry) applyCatalog(ctx context.Context, origin, network string, results []observation) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	protected, existing, err := discoveryInventory(ctx, tx, origin)
	if err != nil {
		return 0, err
	}
	keep := map[NodeEndpoint]bool{}
	for _, obs := range results {
		if obs.height == 0 {
			if e, known := existing[obs.endpoint]; known && !protected[obs.endpoint] && origin == "comparenodes" {
				keep[e] = true
			}
			continue
		}
		key := obs.endpoint.identity()
		if protected[key] {
			continue
		}
		e, ok := existing[key]
		if !ok {
			e = key
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO rpc_nodes(network,protocol,url,origin,status,height,checked_at,latency)
        VALUES(?,?,?,?,'ok',?,?,?) ON CONFLICT(network,protocol,url) DO UPDATE SET
        status='ok',height=excluded.height,checked_at=excluded.checked_at,latency=excluded.latency
        WHERE origin=excluded.origin AND removed=0 AND (checked_at IS NULL OR checked_at<=excluded.checked_at)`, e.Network, e.Protocol, e.URL, origin, obs.height, obs.at.UnixMilli(), obs.latency.Milliseconds())
		if err != nil {
			return 0, err
		}
		keep[e] = true
	}
	// Retire only this directory's rows in the refreshed network. Operator deletions remain
	// tombstones, so a future catalog cannot resurrect an explicitly removed URL.
	rows, err := tx.QueryContext(ctx, "SELECT id,network,protocol,url FROM rpc_nodes WHERE origin=? AND removed=0 AND enabled=1 AND managed=0 AND (?='' OR network=?)", origin, network, network)
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
