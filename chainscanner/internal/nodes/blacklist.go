package nodes

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
)

var errConfiguredRule = errors.New("blacklist entry is managed by the configuration file")

// A snapshot is shared by scan workers, discovery and transaction lookups.
// Writers publish a new map only after SQLite has committed the change.
type hostBlacklist struct {
	writers sync.Mutex
	config  blockedHosts
	current atomic.Pointer[blockedHosts]
}

func (b *hostBlacklist) hosts() *blockedHosts { return b.current.Load() }

func (b *hostBlacklist) load(ctx context.Context, tx *sql.Tx, config blockedHosts) error {
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS node_blacklist (host TEXT PRIMARY KEY)"); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT host FROM node_blacklist")
	if err != nil {
		return err
	}
	defer rows.Close()
	rules := maps.Clone(config.rules)
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return err
		}
		rules[host] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	b.config = config
	snapshot := compileBlacklist(rules)
	b.current.Store(&snapshot)
	return nil
}

func (r *NodeRegistry) setBlocked(ctx context.Context, raw string, block bool) error {
	host, err := normalizeRule(raw)
	if err != nil {
		return err
	}
	b := &r.blacklist
	b.writers.Lock()
	defer b.writers.Unlock()
	if !block && b.config.rules[host] {
		return errConfiguredRule
	}
	current := b.hosts().rules
	if current[host] == block {
		return nil
	}
	next := maps.Clone(current)
	if block {
		_, err = r.db.ExecContext(ctx, "INSERT INTO node_blacklist(host) VALUES(?) ON CONFLICT DO NOTHING", host)
		next[host] = true
	} else {
		_, err = r.db.ExecContext(ctx, "DELETE FROM node_blacklist WHERE host=?", host)
		delete(next, host)
	}
	if err != nil {
		return err
	}
	snapshot := compileBlacklist(next)
	b.current.Store(&snapshot)
	r.revision.Add(1)
	return nil
}

func (r *NodeRegistry) registerBlacklistRoutes(mux *http.ServeMux) {
	type entry struct {
		Host   string `json:"host"`
		Source string `json:"source"`
	}
	mux.HandleFunc("GET /v1/admin/blacklist", func(w http.ResponseWriter, req *http.Request) {
		hosts := slices.Sorted(maps.Keys(r.blacklist.hosts().rules))
		entries := make([]entry, 0, len(hosts))
		for _, host := range hosts {
			source := "admin"
			if r.blacklist.config.rules[host] {
				source = "config"
			}
			entries = append(entries, entry{host, source})
		}
		nodeReply(w, http.StatusOK, entries, nil)
	})
	change := func(w http.ResponseWriter, req *http.Request) {
		err := r.setBlocked(req.Context(), req.PathValue("host"), req.Method == http.MethodPut)
		nodeReply(w, http.StatusNoContent, nil, err)
	}
	mux.HandleFunc("PUT /v1/admin/blacklist/{host}", change)
	mux.HandleFunc("DELETE /v1/admin/blacklist/{host}", change)
}
