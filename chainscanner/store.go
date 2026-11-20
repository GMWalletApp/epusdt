// Package scanner maintains one persistent chain cursor and fans observed
// transactions out to independent, authenticated consumers.
package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Watch struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

// Activity carries chain evidence, including failed and outgoing transactions.
// A subscriber is responsible for interpreting it.
type Activity struct {
	Confirmations int64           `json:"confirmations"`
	Network       string          `json:"network"`
	Height        int64           `json:"height"`
	TxID          string          `json:"tx_id"`
	TimeMS        int64           `json:"time_ms"`
	Addresses     []string        `json:"addresses"`
	Data          json.RawMessage `json:"data"`
}
type Delivery struct {
	ID       int64    `json:"id"`
	Activity Activity `json:"activity"`
}
type Store struct {
	waitMu  sync.Mutex
	waiters map[queueKey]chan struct{}
	db      *sql.DB
	workers sync.Map
	indexes sync.Map // network -> immutable address index, loaded inside the SQLite transaction
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA locking_mode=EXCLUSIVE;
 PRAGMA journal_mode=WAL;
 PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS watches(consumer TEXT NOT NULL, network TEXT NOT NULL, address TEXT NOT NULL, PRIMARY KEY(consumer,network,address));
 CREATE INDEX IF NOT EXISTS by_address ON watches(network,address);
 CREATE TABLE IF NOT EXISTS cursors(network TEXT PRIMARY KEY,height INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS deliveries(id INTEGER PRIMARY KEY AUTOINCREMENT, consumer TEXT NOT NULL, payload BLOB NOT NULL);
 CREATE INDEX IF NOT EXISTS by_consumer ON deliveries(consumer,id);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	var columns int
	if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('watches') WHERE name='from_height'").Scan(&columns); err == nil && columns == 0 {
		_, err = db.Exec("ALTER TABLE watches ADD COLUMN from_height INTEGER")
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	// Keep outstanding v1 deliveries and their IDs across upgrades.
	if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('deliveries') WHERE name='network'").Scan(&columns); err == nil && columns == 0 {
		_, err = db.Exec("ALTER TABLE deliveries ADD COLUMN network TEXT")
	}
	if err == nil {
		_, err = db.Exec("UPDATE deliveries SET network=json_extract(CAST(payload AS TEXT),'$.network') WHERE network IS NULL; CREATE INDEX IF NOT EXISTS by_network_delivery ON deliveries(consumer,network,id)")
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, waiters: map[queueKey]chan struct{}{}}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Subscribe(ctx context.Context, consumer string, watches []Watch) error {
	_, err := s.register(ctx, consumer, watches, false)
	return err
}

var ErrCursorNotReady = errors.New("network cursor has not initialized")

type Registration struct {
	Watch
	FromHeight int64 `json:"from_height"`
}

// SubscribeFrom returns inclusive, durable per-address boundaries. A complete
// replacement succeeds only if every requested chain has initialized.
func (s *Store) SubscribeFrom(ctx context.Context, consumer string, watches []Watch) ([]Registration, error) {
	return s.register(ctx, consumer, watches, true)
}
func (s *Store) register(ctx context.Context, consumer string, watches []Watch, requireHeight bool) ([]Registration, error) {
	if strings.TrimSpace(consumer) == "" {
		return nil, errors.New("empty consumer")
	}
	wanted := make(map[Watch]bool, len(watches))
	ordered := make([]Watch, 0, len(watches))
	for _, w := range watches {
		a, err := Canonical(w.Network, w.Address)
		if err != nil {
			return nil, err
		}
		w.Address = a
		if !wanted[w] {
			wanted[w] = true
			ordered = append(ordered, w)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT network,address,from_height FROM watches WHERE consumer=?", consumer)
	if err != nil {
		return nil, err
	}
	old := make(map[Watch]sql.NullInt64)
	for rows.Next() {
		var w Watch
		var start sql.NullInt64
		if err = rows.Scan(&w.Network, &w.Address, &start); err != nil {
			rows.Close()
			return nil, err
		}
		old[w] = start
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for w := range old {
		if !wanted[w] {
			if _, err = tx.ExecContext(ctx, "DELETE FROM watches WHERE consumer=? AND network=? AND address=?", consumer, w.Network, w.Address); err != nil {
				return nil, err
			}
			s.indexes.Delete(w.Network)
		}
	}
	receipts := make([]Registration, 0, len(ordered))
	next := map[string]sql.NullInt64{}
	for _, w := range ordered {
		start, exists := old[w]
		if !start.Valid {
			var cached bool
			start, cached = next[w.Network]
			if !cached {
				err = tx.QueryRowContext(ctx, "SELECT height+1 FROM cursors WHERE network=?", w.Network).Scan(&start)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
				next[w.Network] = start
			}
		}
		if !start.Valid && requireHeight {
			return nil, fmt.Errorf("%s: %w", w.Network, ErrCursorNotReady)
		}
		if !exists || (!old[w].Valid && start.Valid) {
			if _, err = tx.ExecContext(ctx, "INSERT INTO watches(consumer,network,address,from_height) VALUES(?,?,?,?) ON CONFLICT(consumer,network,address) DO UPDATE SET from_height=excluded.from_height", consumer, w.Network, w.Address, start); err != nil {
				return nil, err
			}
			if !exists {
				s.indexes.Delete(w.Network)
			}
		}
		if start.Valid {
			receipts = append(receipts, Registration{w, start.Int64})
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return receipts, nil
}
func (s *Store) Pending(ctx context.Context, consumer string) (*Delivery, error) {
	return s.pending(ctx, consumer, "")
}
func (s *Store) pending(ctx context.Context, consumer, network string) (*Delivery, error) {
	var d Delivery
	var b []byte
	sqlText := "SELECT id,payload FROM deliveries WHERE consumer=?"
	args := []any{consumer}
	if network != "" {
		sqlText += " AND network=?"
		args = append(args, network)
	}
	err := s.db.QueryRowContext(ctx, sqlText+" ORDER BY id LIMIT 1", args...).Scan(&d.ID, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &d.Activity); err != nil {
		return nil, err
	}
	return &d, nil
}

// PendingWait parks an idle subscriber outside SQLite and rechecks the durable
// queue on notification. Registering before checking closes the lost-wakeup race.
func (s *Store) PendingWait(ctx context.Context, consumer string, wait time.Duration) (*Delivery, error) {
	return s.PendingNetwork(ctx, consumer, "", wait)
}

type queueKey struct{ consumer, network string }

func (s *Store) PendingNetwork(ctx context.Context, consumer, network string, wait time.Duration) (*Delivery, error) {
	if wait <= 0 {
		return s.pending(ctx, consumer, network)
	}
	key := queueKey{consumer, network}
	s.waitMu.Lock()
	ch := s.waiters[key]
	if ch == nil {
		ch = make(chan struct{})
		s.waiters[key] = ch
	}
	s.waitMu.Unlock()
	d, err := s.pending(ctx, consumer, network)
	if d != nil || err != nil {
		return d, err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	case <-ch:
		return s.pending(ctx, consumer, network)
	}
}

func (s *Store) Ack(ctx context.Context, consumer string, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM deliveries WHERE consumer=? AND id=?", consumer, id)
	return err
}
func (s *Store) cursor(ctx context.Context, network string) (int64, bool, error) {
	var height int64
	err := s.db.QueryRowContext(ctx, "SELECT height FROM cursors WHERE network=?", network).Scan(&height)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return height, err == nil, err
}
func (s *Store) commit(ctx context.Context, network string, prev, end int64, events []Activity) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE cursors SET height=? WHERE network=? AND height=?", end, network, prev)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("cursor changed for %s", network)
	}
	// Match against the subscription set at commit time. Registration and block
	// delivery are serialized by SQLite. Each consumer gets one event per tx.
	watchers, err := s.addressIndex(ctx, tx, network)
	if err != nil {
		return err
	}
	if len(watchers) == 0 || len(events) == 0 {
		return tx.Commit()
	}
	insert, err := tx.PrepareContext(ctx, "INSERT INTO deliveries(consumer,network,payload) VALUES(?,?,?)")
	if err != nil {
		return err
	}
	defer insert.Close()
	notify := map[string]bool{}
	for _, e := range events {
		targets := map[string][]string{}
		seen := map[string]bool{}
		for _, a := range e.Addresses {
			if seen[a] {
				continue
			}
			seen[a] = true
			for _, c := range watchers[a] {
				targets[c] = append(targets[c], a)
			}
		}
		for c, matched := range targets {
			notify[c] = true
			e.Network = network
			e.Addresses = matched
			b, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if _, err = insert.ExecContext(ctx, c, network, b); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.waitMu.Lock()
	for c := range notify {
		for _, key := range []queueKey{{c, ""}, {c, network}} {
			if ch := s.waiters[key]; ch != nil {
				close(ch)
				delete(s.waiters, key)
			}
		}
	}
	s.waitMu.Unlock()
	return nil
}

// Access under the store's single SQLite connection orders index invalidation
// with watch mutations and block commits, including rolled-back registrations.
func (s *Store) addressIndex(ctx context.Context, tx *sql.Tx, network string) (map[string][]string, error) {
	if v, ok := s.indexes.Load(network); ok {
		return v.(map[string][]string), nil
	}
	watchers := map[string][]string{}
	rows, err := tx.QueryContext(ctx, "SELECT address,consumer FROM watches WHERE network=?", network)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a, c string
		if err = rows.Scan(&a, &c); err != nil {
			rows.Close()
			return nil, err
		}
		watchers[a] = append(watchers[a], c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	s.indexes.Store(network, watchers)
	return watchers, nil
}

// SeedCursor imports a last processed height only on the first start.
func (s *Store) SeedCursor(ctx context.Context, network string, height int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO cursors(network,height) VALUES(?,?)", network, height); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE watches SET from_height=(SELECT height+1 FROM cursors WHERE network=?) WHERE network=? AND from_height IS NULL", network, network); err != nil {
		return err
	}
	return tx.Commit()
}

// Height returns the last fully committed height or -1 before first use.
func (s *Store) Height(ctx context.Context, network string) (int64, error) {
	h, ok, e := s.cursor(ctx, network)
	if !ok {
		return -1, e
	}
	return h, e
}
