package scanner

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Node struct {
	ID int64 `json:"id"`
	NodeEndpoint
	Origin     string `json:"origin"`
	Status     string `json:"status"`
	Height     *int64 `json:"height"`
	CheckedAt  *int64 `json:"checked_at_ms"`
	Latency    *int64 `json:"latency_ms"`
	HeightUnit string `json:"height_unit"`
	options    NodeOptions
	generation int64
	Scannable  bool `json:"scannable"`
}

// NodeOptions accepts the same credentials and companion endpoint as a network configuration.
type NodeOptions struct {
	NodeEndpoint
	APIKey        string `json:"api_key,omitempty"`
	HistoryURL    string `json:"history_url,omitempty"`
	HistoryAPIKey string `json:"history_api_key,omitempty"`
}

type NodeRegistry struct {
	store      *Store
	configured map[NodeEndpoint]Network
	slots      chan struct{}
	batch      sync.Mutex
	revision   atomic.Uint64
}

func NewNodeRegistry(ctx context.Context, s *Store, networks []Network) (*NodeRegistry, error) {
	r := &NodeRegistry{store: s, configured: map[NodeEndpoint]Network{}, slots: make(chan struct{}, 8)}
	enabled := map[string]Network{}
	for _, n := range networks {
		if !Supported(n.Name) {
			return nil, errors.New("node requires a concrete supported network")
		}
		if _, ok := enabled[n.Name]; ok {
			return nil, errors.New("duplicate network")
		}
		enabled[n.Name] = n
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS rpc_nodes (
	id INTEGER PRIMARY KEY AUTOINCREMENT, network TEXT NOT NULL, protocol TEXT NOT NULL, url TEXT NOT NULL,
	api_key TEXT NOT NULL DEFAULT '', history_url TEXT NOT NULL DEFAULT '', history_key TEXT NOT NULL DEFAULT '',
	origin TEXT NOT NULL, removed INTEGER NOT NULL DEFAULT 0, height INTEGER, checked_at INTEGER,
	latency INTEGER, generation INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'untested', UNIQUE(network,protocol,url))`)
	if err != nil {
		return nil, err
	}
	// Rows originally supplied by a removed config entry stop following it.
	// Built-ins and custom entries keep their own lifecycle.
	for _, e := range builtinNodes {
		if _, err = tx.ExecContext(ctx, seedNode, e.Network, e.Protocol, e.URL, "builtin"); err != nil {
			return nil, err
		}
	}
	for _, n := range networks {
		if n.URL == "" {
			continue
		}
		_, protocol, err := n.endpoint()
		if err != nil {
			return nil, err
		}
		e := NodeEndpoint{n.Name, protocol, n.URL}
		r.configured[e] = n
		if _, err = tx.ExecContext(ctx, seedNode, e.Network, e.Protocol, e.URL, "config"); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.configured = enabled
	s.resolve = func(ctx context.Context, name string) (int64, error) {
		n, ok := enabled[name]
		if !ok {
			return 0, ErrNetworkNotConfigured
		}
		pool := &nodePool{registry: r, settings: n, lifetime: ctx}
		defer pool.Close()
		attempted := map[int64]bool{}
		for ctx.Err() == nil {
			tip, e := pool.Head(ctx)
			if e == nil {
				return activationCursor(tip, n), nil
			}
			if pool.id == 0 || attempted[pool.id] {
				return 0, e
			}
			attempted[pool.id] = true
			pool.Close()
		}
		return 0, ctx.Err()
	}
	return r, nil
}

const seedNode = `INSERT INTO rpc_nodes(network,protocol,url,origin) VALUES(?,?,?,?)
ON CONFLICT(network,protocol,url) DO NOTHING`
const nodeFields = `id,network,protocol,url,origin,status,height,checked_at,latency,generation,api_key,history_url,history_key`

func (r *NodeRegistry) scan(row interface{ Scan(...any) error }) (Node, error) {
	var n Node
	err := row.Scan(&n.ID, &n.Network, &n.Protocol, &n.URL, &n.Origin, &n.Status, &n.Height, &n.CheckedAt, &n.Latency, &n.generation, &n.options.APIKey, &n.options.HistoryURL, &n.options.HistoryAPIKey)
	n.Scannable = Supported(n.Network)
	switch {
	case n.Protocol == "sse":
		n.HeightUnit = "beacon_slot"
	case n.Network == "solana":
		n.HeightUnit = "slot"
	case n.Network == "aptos":
		n.HeightUnit = "ledger_version"
	case n.Network == "sui":
		n.HeightUnit = "checkpoint"
	default:
		n.HeightUnit = "block"
	}
	return n, err
}

func (r *NodeRegistry) List(ctx context.Context, network string) ([]Node, error) {
	rows, err := r.store.db.QueryContext(ctx, `SELECT `+nodeFields+` FROM rpc_nodes WHERE removed=0 AND (?='' OR network=?) ORDER BY id`, network, network)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		n, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		if r.visible(n) {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}
func (r *NodeRegistry) visible(n Node) bool {
	_, configured := r.configured[n.NodeEndpoint]
	if n.Origin == "builtin" && !configured {
		return slices.Contains(builtinNodes, n.NodeEndpoint)
	}
	return n.Origin != "config" || configured
}
func (r *NodeRegistry) get(ctx context.Context, id int64) (Node, error) {
	n, err := r.scan(r.store.db.QueryRowContext(ctx, `SELECT `+nodeFields+` FROM rpc_nodes WHERE id=? AND removed=0`, id))
	if err == nil && !r.visible(n) {
		return Node{}, sql.ErrNoRows
	}
	return n, err
}

var errInvalidNode = errors.New("invalid node endpoint")
var errDuplicateNode = errors.New("node already exists")

func (r *NodeRegistry) Add(ctx context.Context, options NodeOptions) (Node, error) {
	e := options.NodeEndpoint
	e.URL = strings.TrimSpace(e.URL)
	u, err := url.Parse(e.URL)
	if err != nil || u.User != nil || u.Fragment != "" || len(e.URL) > 4096 {
		return Node{}, errInvalidNode
	}
	if e.Network == "bitcoin" {
		if u.Hostname() == "" || options.HistoryURL != "" || options.HistoryAPIKey != "" || e.Protocol != "unisat" || (u.Scheme != "https" && u.Scheme != "http") {
			return Node{}, errInvalidNode
		}
	} else {
		net := Network{Name: e.Network, URL: e.URL, Protocol: e.Protocol, HistoryURL: options.HistoryURL, HistoryAPIKey: options.HistoryAPIKey}
		_, p, err := net.endpoint()
		if err != nil || ValidateNetwork(net) != nil {
			return Node{}, errInvalidNode
		}
		e.Protocol = p
	}
	_, configured := r.configured[e]
	var id int64
	err = r.store.db.QueryRowContext(ctx, `INSERT INTO rpc_nodes(network,protocol,url,api_key,history_url,history_key,origin) VALUES(?,?,?,?,?,?,'custom')
	ON CONFLICT(network,protocol,url) DO UPDATE SET api_key=excluded.api_key,history_url=excluded.history_url,history_key=excluded.history_key,generation=generation+1, removed=0, status='untested',height=NULL,checked_at=NULL,latency=NULL,origin=CASE WHEN origin='builtin' THEN origin ELSE 'custom' END
	WHERE removed=1 OR (origin='config' AND NOT ?) RETURNING id`, e.Network, e.Protocol, e.URL, options.APIKey, options.HistoryURL, options.HistoryAPIKey, configured).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, errDuplicateNode
	}
	if err != nil {
		return Node{}, err
	}
	r.revision.Add(1)
	return r.get(ctx, id)
}
func (r *NodeRegistry) Delete(ctx context.Context, id int64) error {
	if _, err := r.get(ctx, id); err != nil {
		return err
	}
	res, err := r.store.db.ExecContext(ctx, `UPDATE rpc_nodes SET removed=1 WHERE id=? AND removed=0`, id)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	r.revision.Add(1)
	return err
}

// Only the exact configured endpoint gets its operator-supplied credentials and history URL.
func (r *NodeRegistry) network(n Node, settings Network) Network {
	src := Network{Name: n.Network, URL: n.URL, Protocol: n.Protocol, APIKey: n.options.APIKey, HistoryURL: n.options.HistoryURL, HistoryAPIKey: n.options.HistoryAPIKey}
	if configured, ok := r.configured[n.NodeEndpoint]; ok {
		src = configured
		src.Protocol = n.Protocol
	}
	src.Confirmations = settings.Confirmations
	src.Lookback = settings.Lookback
	src.IntervalSeconds = settings.IntervalSeconds
	src.TraceCalls = settings.TraceCalls
	return src
}

func (r *NodeRegistry) Test(ctx context.Context, id int64) (Node, error) {
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return Node{}, ctx.Err()
	}
	n, err := r.get(ctx, id)
	if err != nil {
		return Node{}, err
	}
	start := time.Now()
	timeout := 10 * time.Second
	if n.Protocol == "sse" {
		timeout = 25 * time.Second
	}
	probe, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	src := r.network(n, Network{})
	var height int64
	if n.Protocol == "unisat" {
		height, err = unisatHeight(probe, src)
	} else {
		var s Source
		s, err = NewSource(probe, src)
		if err == nil {
			height, err = nodeHead(probe, s, src)
			s.Close()
		}
	}
	// Do not write a disconnected admin client's canceled observation.
	if ctx.Err() != nil {
		return Node{}, ctx.Err()
	}
	status := "ok"
	var observed any = height
	if err != nil || height < 0 {
		status = "unavailable"
		if errors.Is(probe.Err(), context.DeadlineExceeded) {
			status = "timeout"
		}
		observed = nil
	}
	_, err = r.store.db.ExecContext(ctx, `UPDATE rpc_nodes SET status=?,height=?,checked_at=?,latency=? WHERE id=? AND removed=0 AND generation=? AND (checked_at IS NULL OR checked_at<=?)`, status, observed, start.UnixMilli(), time.Since(start).Milliseconds(), id, n.generation, start.UnixMilli())
	if err != nil {
		return Node{}, err
	}
	return r.get(ctx, id)
}

func unisatHeight(ctx context.Context, n Network) (int64, error) {
	s := &jsonSource{n: n, http: newNodeHTTPClient(10 * time.Second)}
	defer s.Close()
	v, err := s.request(ctx, "/v1/indexer/blockchain/info", nil)
	if err != nil {
		return 0, err
	}
	if v.Get("code").Raw != "0" || v.Get("data.chain").String() != "main" {
		return 0, errors.New("invalid Bitcoin mainnet response")
	}
	return strconv.ParseInt(v.Get("data.blocks").Raw, 10, 64)
}
