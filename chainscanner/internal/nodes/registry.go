package nodes

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
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
	db         *sql.DB
	store      *engine.Store
	configured map[NodeEndpoint]chains.Network
	slots      chan struct{}
	batch      sync.Mutex
	revision   atomic.Uint64
}

func NewNodeRegistry(ctx context.Context, s *engine.Store, networks []chains.Network) (*NodeRegistry, error) {
	r := &NodeRegistry{store: s, db: engine.Database(s), configured: map[NodeEndpoint]chains.Network{}, slots: make(chan struct{}, 8)}
	enabled := map[string]chains.Network{}
	for _, n := range networks {
		if !chains.Supported(n.Name) {
			return nil, errors.New("node requires a concrete supported network")
		}
		if _, ok := enabled[n.Name]; ok {
			return nil, errors.New("duplicate network")
		}
		enabled[n.Name] = n
	}
	tx, err := r.db.BeginTx(ctx, nil)
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
		protocol, err := chains.EndpointProtocol(n)
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
	engine.ConfigureNetworks(s, enabled, func(ctx context.Context, name string) (int64, error) {
		n, ok := enabled[name]
		if !ok {
			return 0, engine.ErrNetworkNotConfigured
		}
		pool := newRegistryPool(ctx, r, n)
		defer pool.Close()
		tip, err := pool.Head(ctx)
		if err != nil {
			return 0, err
		}
		return engine.ActivationCursor(tip, n), nil
	})
	return r, nil
}

const seedNode = `INSERT INTO rpc_nodes(network,protocol,url,origin) VALUES(?,?,?,?)
ON CONFLICT(network,protocol,url) DO UPDATE SET origin=excluded.origin WHERE origin='chainlist'`
const nodeFields = `id,network,protocol,url,origin,status,height,checked_at,latency,generation,api_key,history_url,history_key`

func (r *NodeRegistry) scan(row interface{ Scan(...any) error }) (Node, error) {
	var n Node
	err := row.Scan(&n.ID, &n.Network, &n.Protocol, &n.URL, &n.Origin, &n.Status, &n.Height, &n.CheckedAt, &n.Latency, &n.generation, &n.options.APIKey, &n.options.HistoryURL, &n.options.HistoryAPIKey)
	n.Scannable = chains.Supported(n.Network)
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
	rows, err := r.db.QueryContext(ctx, `SELECT `+nodeFields+` FROM rpc_nodes WHERE removed=0 AND (?='' OR network=?) ORDER BY id`, network, network)
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
	n, err := r.scan(r.db.QueryRowContext(ctx, `SELECT `+nodeFields+` FROM rpc_nodes WHERE id=? AND removed=0`, id))
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
		net := chains.Network{Name: e.Network, URL: e.URL, Protocol: e.Protocol, HistoryURL: options.HistoryURL, HistoryAPIKey: options.HistoryAPIKey}
		p, err := chains.EndpointProtocol(net)
		if err != nil || chains.ValidateNetwork(net) != nil {
			return Node{}, errInvalidNode
		}
		e.Protocol = p
	}
	_, configured := r.configured[e]
	var id int64
	err = r.db.QueryRowContext(ctx, `INSERT INTO rpc_nodes(network,protocol,url,api_key,history_url,history_key,origin) VALUES(?,?,?,?,?,?,'custom')
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
	res, err := r.db.ExecContext(ctx, `UPDATE rpc_nodes SET removed=1 WHERE id=? AND removed=0`, id)
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
func (r *NodeRegistry) network(n Node, settings chains.Network) chains.Network {
	src := chains.Network{Name: n.Network, URL: n.URL, Protocol: n.Protocol, APIKey: n.options.APIKey, HistoryURL: n.options.HistoryURL, HistoryAPIKey: n.options.HistoryAPIKey}
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

func openNode(ctx context.Context, n Node, network chains.Network) (chains.Source, error) {
	if n.Origin == "chainlist" {
		return chains.NewPublicSource(ctx, network)
	}
	return chains.NewSource(ctx, network)
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
	src := r.network(n, chains.Network{})
	var height int64
	if n.Protocol == "unisat" {
		height, err = chains.UniSatHeight(probe, src)
	} else {
		var s chains.Source
		s, err = openNode(probe, n, src)
		if err == nil {
			height, err = chains.NodeHead(probe, s, src)
			s.Close()
		}
	}
	// Do not write a disconnected admin client's canceled observation.
	if ctx.Err() != nil {
		return Node{}, ctx.Err()
	}
	if errors.Is(probe.Err(), context.DeadlineExceeded) {
		err = probe.Err()
	}
	err = r.saveHead(ctx, n, headSample{start: start, latency: time.Since(start), height: height, err: err})
	if err != nil {
		return Node{}, err
	}
	return r.get(ctx, id)
}
