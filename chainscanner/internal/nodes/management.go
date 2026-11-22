package nodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

var errDisabledNode = errors.New("node is disabled")

func upgradeNodeSettings(ctx context.Context, tx *sql.Tx) error {
	var installed bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_table_info('rpc_nodes') WHERE name='enabled')").Scan(&installed); err != nil {
		return err
	}
	if !installed {
		for _, statement := range []string{
			"ALTER TABLE rpc_nodes ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1",
			"ALTER TABLE rpc_nodes ADD COLUMN weight INTEGER NOT NULL DEFAULT 1",
			"ALTER TABLE rpc_nodes ADD COLUMN purpose TEXT NOT NULL DEFAULT 'general'",
			"ALTER TABLE rpc_nodes ADD COLUMN role TEXT NOT NULL DEFAULT 'regular'",
			"ALTER TABLE rpc_nodes ADD COLUMN managed INTEGER NOT NULL DEFAULT 0",
			"UPDATE rpc_nodes SET role='fallback' WHERE origin='fallback'",
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS registry_identity (id INTEGER PRIMARY KEY CHECK(id=1), value TEXT NOT NULL);
    INSERT OR IGNORE INTO registry_identity VALUES(1,lower(hex(randomblob(16))))`)
	return err
}

// Identity preserves credential-bearing paths and queries while matching equivalent transports.
func (e NodeEndpoint) Identity() NodeEndpoint {
	e.URL = strings.TrimSpace(e.URL)
	if p, err := chains.EndpointProtocol(chains.Network{Name: e.Network, Protocol: e.Protocol, URL: e.URL}); err == nil {
		e.Protocol = p
	}
	return e.identity()
}

type NodePatch struct {
	URL           *string `json:"url"`
	APIKey        *string `json:"api_key"`
	HistoryURL    *string `json:"history_url"`
	HistoryAPIKey *string `json:"history_api_key"`
	Enabled       *bool   `json:"enabled"`
	Purpose       *string `json:"purpose"`
	Weight        *int    `json:"weight"`
	Role          *string `json:"role"`
}

func (r *NodeRegistry) update(ctx context.Context, id int64, p NodePatch) (Node, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	n, err := r.scan(tx.QueryRowContext(ctx, "SELECT "+nodeFields+" FROM rpc_nodes WHERE id=? AND removed=0", id))
	if err != nil {
		return Node{}, err
	}
	if !r.visible(n) {
		return Node{}, sql.ErrNoRows
	}
	cfg := r.network(n, chains.Network{})
	o := NodeOptions{NodeEndpoint: n.NodeEndpoint, APIKey: cfg.APIKey, HistoryURL: cfg.HistoryURL, HistoryAPIKey: cfg.HistoryAPIKey, Weight: n.Weight, Purpose: n.Purpose}
	if p.URL != nil {
		o.URL = *p.URL
	}
	if p.APIKey != nil {
		o.APIKey = *p.APIKey
	}
	if p.HistoryURL != nil {
		o.HistoryURL = *p.HistoryURL
	}
	if p.HistoryAPIKey != nil {
		o.HistoryAPIKey = *p.HistoryAPIKey
	}
	if p.Weight != nil {
		o.Weight = *p.Weight
	}
	if p.Purpose != nil {
		o.Purpose = *p.Purpose
	}
	enabled, role := n.Enabled, n.Role
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	if p.Role != nil {
		role = *p.Role
	}
	if role != "regular" && role != "fallback" {
		return Node{}, errInvalidNode
	}
	e := n.NodeEndpoint
	if o.Weight < 1 || (o.Purpose != "both" && o.Purpose != "general" && o.Purpose != "manual_verify") || role == "fallback" && (o.Purpose == "manual_verify" || !chains.Supported(n.Network)) {
		return Node{}, errInvalidNode
	}
	if p.URL != nil || p.HistoryURL != nil || p.HistoryAPIKey != nil {
		e, err = r.validate(o, role == "fallback")
		if err != nil {
			return Node{}, err
		}
	}
	origin := n.Origin
	if e != n.NodeEndpoint {
		peers, err := loadSeedRows(ctx, tx)
		if err != nil {
			return Node{}, err
		}
		for _, peer := range peers[e.identity()] {
			if peer.id != id {
				return Node{}, errDuplicateNode
			}
		}
		origin = "custom"
		if role == "fallback" {
			origin = "fallback"
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE rpc_nodes SET url=?, protocol=?,origin=?,managed=1,enabled=?,role=?,purpose=?,weight=?,api_key=?,history_url=?,history_key=?,generation=generation+1,status='untested',checked_at=NULL,height=NULL,latency=NULL WHERE id=?`, e.URL, e.Protocol, origin, enabled, role, o.Purpose, o.Weight, o.APIKey, o.HistoryURL, o.HistoryAPIKey, id)
	if err != nil {
		return Node{}, err
	}
	if e.identity() != n.NodeEndpoint.identity() {
		_, err = tx.ExecContext(ctx, "INSERT INTO rpc_nodes(network,protocol,url,origin,removed) VALUES(?,?,?,?,1)", n.Network, n.Protocol, n.URL, n.Origin)
		if err != nil {
			return Node{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Node{}, err
	}
	r.changed(n.Network)
	return r.get(ctx, id)
}

// Private inventory is available only to the service holding the administrator credential.
// EPUSDT deliberately excludes this path from its browser proxy.
type Route struct {
	Node
	APIKey        string `json:"api_key,omitempty"`
	HistoryURL    string `json:"history_url,omitempty"`
	HistoryAPIKey string `json:"history_api_key,omitempty"`
}

type RouteInventory struct {
	StoreID string  `json:"store_id"`
	Nodes   []Route `json:"nodes"`
}

func (r *NodeRegistry) routes(ctx context.Context, network, use string) (RouteInventory, error) {
	var out RouteInventory
	if network == "" || use != "manual" && use != "general" {
		return out, errInvalidNode
	}
	if err := r.db.QueryRowContext(ctx, "SELECT value FROM registry_identity WHERE id=1").Scan(&out.StoreID); err != nil {
		return out, err
	}
	rows, err := r.List(ctx, network)
	if err != nil {
		return out, err
	}
	out.Nodes = []Route{}
	for _, n := range rows {
		if isDiscoveryOrigin(n.Origin) || (!n.Enabled || (n.Status != "ok" && n.Status != "untested") || (use == "general" && (n.Purpose == "manual_verify" || n.Role == "fallback" || !n.Scannable))) {
			continue
		}
		cfg := r.network(n, chains.Network{})
		out.Nodes = append(out.Nodes, Route{n, cfg.APIKey, cfg.HistoryURL, cfg.HistoryAPIKey})
	}
	return out, nil
}

func (r *NodeRegistry) registerManagement(admin *http.ServeMux) {
	admin.HandleFunc("GET /v1/admin/migration-manifest", func(w http.ResponseWriter, req *http.Request) {
		m, err := r.migrationManifest(req.Context())
		nodeReply(w, 200, m, err)
	})
	admin.HandleFunc("GET /v1/admin/capabilities", func(w http.ResponseWriter, req *http.Request) {
		nodeReply(w, 200, ManagementCapabilities{Protocols: ManagementProtocols(), NodeSettings: true, Migration: true}, nil)
	})
	admin.HandleFunc("PATCH /v1/admin/nodes/{id}", func(w http.ResponseWriter, req *http.Request) {
		id, err := nodeID(req)
		var p NodePatch
		d := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8192))
		d.DisallowUnknownFields()
		if err != nil || d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || p == (NodePatch{}) {
			nodeReply(w, 400, nil, errInvalidNode)
			return
		}
		n, err := r.update(req.Context(), id, p)
		nodeReply(w, 200, n, err)
	})
	admin.HandleFunc("GET /v1/admin/routes", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		result, err := r.routes(req.Context(), strings.TrimSpace(q.Get("network")), q.Get("use"))
		nodeReply(w, 200, result, err)
	})
}

// ManagementCapabilities is the operation set advertised by this scanner.
type ManagementCapabilities struct {
	Protocols    map[string][]string `json:"protocols"`
	NodeSettings bool                `json:"node_settings"`
	Migration    bool                `json:"migration"`
}

func ManagementProtocols() map[string][]string {
	protocols := map[string][]string{"bitcoin": {"unisat", "jsonrpc"}}
	for _, e := range builtinNodes {
		if chains.Supported(e.Network) {
			protocols[e.Network] = chains.Protocols(e.Network)
		}
	}
	protocols["ton"] = append(protocols["ton"], "toncenter-v3")
	return protocols
}
