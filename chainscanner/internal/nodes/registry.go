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
	Enabled     bool   `json:"enabled"`
	Weight      int    `json:"weight"`
	Purpose     string `json:"purpose"`
	Role        string `json:"role"`
	managed     bool
	Origin      string `json:"origin"`
	Status      string `json:"status"`
	ReadFailure string `json:"read_failure,omitempty"`
	ReadRetryAt int64  `json:"read_retry_at_ms,omitempty"`
	Height      *int64 `json:"height"`
	CheckedAt   *int64 `json:"checked_at_ms"`
	Latency     *int64 `json:"latency_ms"`
	HeightUnit  string `json:"height_unit"`
	options     NodeOptions
	generation  int64
	Scannable   bool `json:"scannable"`
}

// NodeOptions accepts the same credentials and companion endpoint as a network configuration.
type NodeOptions struct {
	NodeEndpoint
	Enabled       *bool  `json:"enabled,omitempty"`
	Weight        int    `json:"weight,omitempty"`
	Purpose       string `json:"purpose,omitempty"`
	APIKey        string `json:"api_key,omitempty"`
	HistoryURL    string `json:"history_url,omitempty"`
	HistoryAPIKey string `json:"history_api_key,omitempty"`
}

type NodeRegistry struct {
	scanAttempts map[string]*scanAttempts // fixed at startup, one counter per configured network
	blacklist    hostBlacklist
	lookup       transactionLookup
	db           *sql.DB
	store        *engine.Store
	builtins     map[NodeEndpoint]NodeEndpoint // stored spelling -> built-in route with default history
	configured   map[NodeEndpoint]chains.Network
	slots        chan struct{}
	batch        sync.Mutex
	watchMu      sync.Mutex
	changes      map[string]chan struct{}
	revision     atomic.Uint64
	readFailures sync.Map // nodeVersion -> readFailure for the active watch period
}

func NewNodeRegistry(ctx context.Context, s *engine.Store, networks []chains.Network, blockedDomains ...string) (*NodeRegistry, error) {
	blocked, err := parseBlockedHosts(blockedDomains)
	if err != nil {
		return nil, err
	}
	r := &NodeRegistry{store: s, db: engine.Database(s), builtins: map[NodeEndpoint]NodeEndpoint{}, configured: map[NodeEndpoint]chains.Network{}, slots: make(chan struct{}, 8)}
	r.scanAttempts = make(map[string]*scanAttempts, len(networks))
	enabled := map[string]chains.Network{}
	for _, n := range networks {
		if !chains.Supported(n.Name) {
			return nil, errors.New("node requires a concrete supported network")
		}
		if _, ok := enabled[n.Name]; ok {
			return nil, errors.New("duplicate network")
		}
		if err := n.ValidateAssetEvents(); err != nil {
			return nil, err
		}
		enabled[n.Name] = n
		r.scanAttempts[n.Name] = new(scanAttempts)
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
	if err := upgradeNodeSettings(ctx, tx); err != nil {
		return nil, err
	}
	if err := r.blacklist.load(ctx, tx, blocked); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS rpc_nodes_active_network ON rpc_nodes(network, id) WHERE removed=0"); err != nil {
		return nil, err
	}
	existing, err := loadSeedRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, e := range builtinNodes {
		stored, err := existing.seed(ctx, tx, e, "builtin")
		if err != nil {
			return nil, err
		}
		r.builtins[stored] = e
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
		stored, err := existing.seed(ctx, tx, e, "config")
		if err != nil {
			return nil, err
		}
		r.configured[stored] = n
	}
	// Explicit configuration has already claimed its rows. Remove obsolete
	// automatic entries left over from a previous catalog.
	for key := range retiredEndpoints {
		for _, row := range existing[key] {
			if row.removed || row.managed || (row.origin != "builtin" && !isDiscoveryOrigin(row.origin)) {
				continue
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM rpc_nodes WHERE id=?", row.id); err != nil {
				return nil, err
			}
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
		return pool.Head(ctx)
	})
	r.lookup.probe = chains.ProbeLookup
	r.lookup.read = chains.LookupTransaction
	engine.SetTransactionHandler(s, r.transactionHandler(enabled))
	return r, nil
}

const seedNode = `INSERT INTO rpc_nodes(network,protocol,url,origin) VALUES(?,?,?,?)
ON CONFLICT(network,protocol,url) DO UPDATE SET origin=excluded.origin WHERE managed=0 AND (origin IN ('chainlist','openchainbench','comparenodes') OR (origin='config' AND excluded.origin='builtin'))`
const nodeFields = `id,network,protocol,url,origin,status,height,checked_at,latency,generation,api_key,history_url,history_key,enabled,weight,purpose,role,managed`

func (r *NodeRegistry) scan(row interface{ Scan(...any) error }) (Node, error) {
	var n Node
	err := row.Scan(&n.ID, &n.Network, &n.Protocol, &n.URL, &n.Origin, &n.Status, &n.Height, &n.CheckedAt, &n.Latency, &n.generation, &n.options.APIKey, &n.options.HistoryURL, &n.options.HistoryAPIKey, &n.Enabled, &n.Weight, &n.Purpose, &n.Role, &n.managed)
	if data, ok := r.readFailures.Load(n.version()); ok {
		f := data.(readFailure)
		n.Status, n.ReadFailure = "unavailable", f.reason
		if f.retry.After(time.Now()) {
			n.ReadRetryAt = f.retry.UnixMilli()
		}
	}
	n.Scannable = n.Protocol != "unisat" && n.Protocol != "toncenter-v3" && chains.Supported(n.Network) && !slices.Contains(heightOnlyNodes, n.NodeEndpoint.identity())
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
	query := "SELECT " + nodeFields + " FROM rpc_nodes WHERE removed=0"
	var args []any
	if network != "" {
		query += " AND network=?"
		args = append(args, network)
	}
	rows, err := r.db.QueryContext(ctx, query+" ORDER BY id", args...)
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
	if blocked := r.blacklist.hosts(); len(blocked.rules) > 0 {
		route := r.network(n, chains.Network{})
		if blocked.matches(route.URL) || blocked.matches(route.HistoryURL) {
			return false
		}
	}
	if !chains.Supported(n.Network) {
		return false
	}
	_, configured := r.configured[n.NodeEndpoint]
	if n.managed {
		return true
	}
	if n.Origin == "builtin" && !configured {
		_, ok := r.builtins[n.NodeEndpoint]
		return ok
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
	return r.add(ctx, options, false)
}

func (r *NodeRegistry) add(ctx context.Context, options NodeOptions, fallback bool) (Node, error) {
	return r.insert(ctx, options, fallback, false)
}
func (r *NodeRegistry) validate(options NodeOptions, fallback bool) (NodeEndpoint, error) {
	if options.Weight < 1 || (options.Purpose != "general" && options.Purpose != "manual_verify" && options.Purpose != "both") || (fallback && options.Purpose == "manual_verify") {
		return NodeEndpoint{}, errInvalidNode
	}
	if fallback && !chains.Supported(options.Network) {
		return NodeEndpoint{}, errInvalidNode
	}
	if fallback && options.Network == "bitcoin" && options.Protocol == "unisat" {
		return NodeEndpoint{}, errInvalidNode
	}
	if blocked := r.blacklist.hosts(); blocked.matches(strings.TrimSpace(options.URL)) || blocked.matches(options.HistoryURL) {
		return NodeEndpoint{}, errInvalidNode
	}
	return options.ValidatedEndpoint()
}

// ValidatedEndpoint checks the primary and history transports, including height-only interfaces.
func (options NodeOptions) ValidatedEndpoint() (NodeEndpoint, error) {
	e := options.NodeEndpoint
	e.URL = strings.TrimSpace(e.URL)
	u, err := url.Parse(e.URL)
	if err != nil || u.User != nil || u.Fragment != "" || len(e.URL) > 4096 {
		return NodeEndpoint{}, errInvalidNode
	}
	if e.Network == "ton" && e.Protocol == "toncenter-v3" {
		if u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || options.HistoryURL != "" || options.HistoryAPIKey != "" {
			return NodeEndpoint{}, errInvalidNode
		}
	} else if e.Network == "bitcoin" && e.Protocol == "unisat" {
		if u.Hostname() == "" || options.HistoryURL != "" || options.HistoryAPIKey != "" || (u.Scheme != "https" && u.Scheme != "http") {
			return NodeEndpoint{}, errInvalidNode
		}
	} else {
		net := chains.Network{Name: e.Network, URL: e.URL, Protocol: e.Protocol, HistoryURL: options.HistoryURL, HistoryAPIKey: options.HistoryAPIKey}
		p, err := chains.EndpointProtocol(net)
		if err != nil || chains.ValidateNetwork(net) != nil {
			return NodeEndpoint{}, errInvalidNode
		}
		e.Protocol = p
	}
	return e, nil
}
func (r *NodeRegistry) insert(ctx context.Context, options NodeOptions, fallback, importing bool) (Node, error) {
	if options.Purpose == "" {
		options.Purpose = "general"
	}
	if options.Weight == 0 {
		options.Weight = 1
	}
	enabled := options.Enabled == nil || *options.Enabled
	origin, role := "custom", "regular"
	if fallback {
		origin, role = "fallback", "fallback"
	}
	e, err := r.validate(options, fallback)
	if err != nil {
		return Node{}, err
	}
	_, configured := r.configured[e]
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	// Check aliases as well as exact URLs, including built-in and discovered nodes.
	rows, err := tx.QueryContext(ctx, "SELECT network,protocol,url,origin FROM rpc_nodes WHERE network=? AND (removed=0 OR ?)", e.Network, importing)
	if err != nil {
		return Node{}, err
	}
	for rows.Next() {
		var peer NodeEndpoint
		var owner string
		if err = rows.Scan(&peer.Network, &peer.Protocol, &peer.URL, &owner); err != nil {
			break
		}
		if peer.identity() == e.identity() {
			if _, current := r.configured[peer]; owner == "config" && !current && !fallback && !importing {
				e = peer
				continue
			}
			err = errDuplicateNode
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return Node{}, err
	}
	if fallback {
		for endpoint := range r.configured {
			if endpoint.identity() == e.identity() {
				return Node{}, errDuplicateNode
			}
		}
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO rpc_nodes(network,protocol,url,api_key,history_url,history_key,origin,enabled,purpose,weight,role) VALUES(?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(network,protocol,url) DO UPDATE SET enabled=excluded.enabled,purpose=excluded.purpose,weight=excluded.weight,role=excluded.role,managed=0,api_key=excluded.api_key,history_url=excluded.history_url,history_key=excluded.history_key,generation=generation+1, removed=0, status='untested',height=NULL,checked_at=NULL,latency=NULL,origin=CASE WHEN origin='builtin' AND excluded.origin='custom' THEN origin ELSE excluded.origin END
	WHERE removed=1 OR (origin='config' AND NOT ?) RETURNING id`, e.Network, e.Protocol, e.URL, options.APIKey, options.HistoryURL, options.HistoryAPIKey, origin, enabled, options.Purpose, options.Weight, role, configured).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, errDuplicateNode
	}
	if err != nil {
		return Node{}, err
	}
	if err = tx.Commit(); err != nil {
		return Node{}, err
	}
	r.revision.Add(1)
	return r.get(ctx, id)
}
func (r *NodeRegistry) Delete(ctx context.Context, id int64) error {
	n, err := r.get(ctx, id)
	if err != nil {
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
	r.changed(n.Network)
	return err
}

// Only the exact configured endpoint gets its operator-supplied credentials and history URL.
func (r *NodeRegistry) network(n Node, settings chains.Network) chains.Network {
	src := chains.Network{Name: n.Network, URL: n.URL, Protocol: n.Protocol, APIKey: n.options.APIKey, HistoryURL: n.options.HistoryURL, HistoryAPIKey: n.options.HistoryAPIKey}
	if configured, ok := r.configured[n.NodeEndpoint]; ok && !n.managed {
		src = configured
		src.Protocol = n.Protocol
	} else if !n.managed && n.Origin == "builtin" && src.HistoryURL == "" {
		src.HistoryURL = builtinHistory[r.builtins[n.NodeEndpoint]]
	}
	src.Confirmations = settings.Confirmations
	src.Lookback = settings.Lookback
	src.IntervalSeconds = settings.IntervalSeconds
	src.AssetEvents = settings.AssetEvents
	src.TraceCalls = settings.TraceCalls
	return src
}

func openNode(ctx context.Context, n Node, network chains.Network) (chains.Source, error) {
	if isDiscoveryOrigin(n.Origin) {
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
	if !n.Enabled {
		return Node{}, errDisabledNode
	}
	start := time.Now()
	timeout := 10 * time.Second
	if n.Protocol == "sse" {
		timeout = 25 * time.Second
	}
	probe, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	src := r.network(n, chains.Network{})
	defer r.lookup.lanes.scan(chains.ProviderKey(src))()
	var height int64
	if n.Protocol == "toncenter-v3" {
		height, err = chains.TONIndexHeight(probe, src)
	} else if n.Network == "bitcoin" && n.Protocol == "jsonrpc" {
		height, err = chains.BitcoinRPCHeight(probe, src)
	} else if n.Protocol == "unisat" {
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
