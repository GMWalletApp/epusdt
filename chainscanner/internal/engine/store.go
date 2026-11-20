package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
	_ "modernc.org/sqlite"
)

type Store struct {
	demandMu   sync.Mutex
	periods    map[string]*demandPeriod
	configured map[string]chains.Network
	resolve    func(context.Context, string) (int64, error)
	waitMu     sync.Mutex
	waiters    map[queueKey]chan struct{}
	db         *sql.DB
	workers    sync.Map
	indexes    sync.Map // network -> immutable address index, loaded inside the SQLite transaction
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
 CREATE TABLE IF NOT EXISTS scan_gaps(network TEXT NOT NULL,first INTEGER NOT NULL,last INTEGER NOT NULL,attempted INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(network,first));
 CREATE INDEX IF NOT EXISTS gap_retry ON scan_gaps(network,attempted,first);
 CREATE UNIQUE INDEX IF NOT EXISTS gap_end ON scan_gaps(network,last);
 CREATE TABLE IF NOT EXISTS scan_edges(network TEXT NOT NULL,height INTEGER NOT NULL,hash TEXT NOT NULL,PRIMARY KEY(network,height));
 CREATE TRIGGER IF NOT EXISTS clear_scan_work AFTER DELETE ON cursors BEGIN
 DELETE FROM scan_gaps WHERE network=OLD.network;
 DELETE FROM scan_edges WHERE network=OLD.network;
 END;
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
	// A delayed ACK from an earlier stateless instance must not address a
	// newly-created delivery with a small, reused autoincrement ID.
	if path == ":memory:" {
		var seed [8]byte
		if _, err = rand.Read(seed[:]); err == nil {
			offset := int64(binary.BigEndian.Uint64(seed[:]) & ((1 << 52) - 1))
			_, err = db.Exec("INSERT INTO sqlite_sequence(name,seq) VALUES('deliveries',?)", offset)
		}
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	_, err = db.Exec("DELETE FROM cursors WHERE NOT EXISTS (SELECT 1 FROM watches WHERE watches.network=cursors.network)")
	if err != nil {
		db.Close()
		return nil, err
	}
	st := &Store{db: db, waiters: map[queueKey]chan struct{}{}, periods: map[string]*demandPeriod{}}
	rows, err := db.Query("SELECT DISTINCT network FROM watches")
	if err != nil {
		db.Close()
		return nil, err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			break
		}
		st.setDemandLocked(name, true)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}
func (s *Store) Close() error {
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	for name := range s.periods {
		s.setDemandLocked(name, false)
	}
	return s.db.Close()
}
func (s *Store) Subscribe(ctx context.Context, consumer string, watches []feed.Watch) error {
	_, err := s.register(ctx, consumer, watches, false)
	return err
}

// SubscribeFrom returns inclusive, durable per-address boundaries. A complete
// replacement succeeds only if every requested chain has initialized.
func (s *Store) SubscribeFrom(ctx context.Context, consumer string, watches []feed.Watch) ([]feed.Registration, error) {
	return s.register(ctx, consumer, watches, true)
}
func (s *Store) register(ctx context.Context, consumer string, watches []feed.Watch, requireHeight bool) ([]feed.Registration, error) {
	if strings.TrimSpace(consumer) == "" {
		return nil, errors.New("empty consumer")
	}
	wanted := make(map[feed.Watch]bool, len(watches))
	ordered := make([]feed.Watch, 0, len(watches))
	for _, w := range watches {
		a, err := chains.Canonical(w.Network, w.Address)
		if err != nil {
			return nil, err
		}
		w.Address = a
		if !wanted[w] {
			wanted[w] = true
			ordered = append(ordered, w)
		}
	}
	// Resolve cold networks outside the transaction. An unrelated slow RPC
	// must not block existing consumers from acknowledging deliveries.
	boundaries := map[string]boundary{}
	checked := map[string]bool{}
	for _, w := range ordered {
		if checked[w.Network] {
			continue
		}
		checked[w.Network] = true
		if requireHeight && s.configured != nil {
			if _, ok := s.configured[w.Network]; !ok {
				return nil, fmt.Errorf("%s: %w", w.Network, ErrNetworkNotConfigured)
			}
		}
		if !requireHeight || s.resolve == nil {
			continue
		}
		_, epoch := s.period(w.Network)
		_, ok, err := s.cursor(ctx, w.Network)
		if err != nil {
			return nil, err
		}
		if !ok {
			h, err := s.resolve(ctx, w.Network)
			if err != nil || h < -1 {
				return nil, fmt.Errorf("%s: %w", w.Network, feed.ErrCursorNotReady)
			}
			boundaries[w.Network] = boundary{epoch, h}
		}
	}
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT network,address,from_height FROM watches WHERE consumer=?", consumer)
	if err != nil {
		return nil, err
	}
	old := make(map[feed.Watch]sql.NullInt64)
	for rows.Next() {
		var w feed.Watch
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
	touched := map[string]bool{}
	for name, b := range boundaries {
		if s.periodLocked(name).changed != b.period {
			var exists bool
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM cursors WHERE network=?)", name).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, feed.ErrCursorNotReady
			}
		} else if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO cursors(network,height) VALUES(?,?)", name, b.height); err != nil {
			return nil, err
		}
	}
	for w := range old {
		if !wanted[w] {
			if _, err = tx.ExecContext(ctx, "DELETE FROM watches WHERE consumer=? AND network=? AND address=?", consumer, w.Network, w.Address); err != nil {
				return nil, err
			}
			touched[w.Network] = true
		}
	}
	receipts := make([]feed.Registration, 0, len(ordered))
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
			return nil, fmt.Errorf("%s: %w", w.Network, feed.ErrCursorNotReady)
		}
		if !exists || (!old[w].Valid && start.Valid) {
			if _, err = tx.ExecContext(ctx, "INSERT INTO watches(consumer,network,address,from_height) VALUES(?,?,?,?) ON CONFLICT(consumer,network,address) DO UPDATE SET from_height=excluded.from_height", consumer, w.Network, w.Address, start); err != nil {
				return nil, err
			}
			touched[w.Network] = true
		}
		if start.Valid {
			receipts = append(receipts, feed.Registration{Watch: w, FromHeight: start.Int64})
		}
	}
	for n := range touched {
		var active bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM watches WHERE network=?)", n).Scan(&active); err != nil {
			return nil, err
		}
		touched[n] = active
		if !active {
			if _, err = tx.ExecContext(ctx, "DELETE FROM cursors WHERE network=?", n); err != nil {
				return nil, err
			}
		}
		s.indexes.Delete(n)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for n, active := range touched {
		s.setDemandLocked(n, active)
	}
	return receipts, nil
}
func (s *Store) Pending(ctx context.Context, consumer string) (*feed.Delivery, error) {
	return s.pending(ctx, consumer, "")
}
func (s *Store) pending(ctx context.Context, consumer, network string) (*feed.Delivery, error) {
	var d feed.Delivery
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
func (s *Store) PendingWait(ctx context.Context, consumer string, wait time.Duration) (*feed.Delivery, error) {
	return s.PendingNetwork(ctx, consumer, "", wait)
}

type queueKey struct{ consumer, network string }

func (s *Store) PendingNetwork(ctx context.Context, consumer, network string, wait time.Duration) (*feed.Delivery, error) {
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
func (s *Store) complete(ctx context.Context, network string, span chains.Span, link chains.BlockLink, events []chains.Activity) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = finishGap(ctx, tx, network, span, link); err != nil {
		return err
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
				if e.Height >= c.from {
					targets[c.consumer] = append(targets[c.consumer], a)
				}
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
type watcher struct {
	consumer string
	from     int64
}

func (s *Store) addressIndex(ctx context.Context, tx *sql.Tx, network string) (map[string][]watcher, error) {
	if v, ok := s.indexes.Load(network); ok {
		return v.(map[string][]watcher), nil
	}
	watchers := map[string][]watcher{}
	rows, err := tx.QueryContext(ctx, "SELECT address,consumer,COALESCE(from_height,0) FROM watches WHERE network=?", network)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a string
		var c watcher
		if err = rows.Scan(&a, &c.consumer, &c.from); err != nil {
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
	s.indexes.Delete(network)
	return tx.Commit()
}

// Height returns the last fully committed height or -1 before first use.
func (s *Store) Height(ctx context.Context, network string) (int64, error) {
	var h int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT min(first)-1 FROM scan_gaps WHERE network=?),(SELECT height FROM cursors WHERE network=?),-1)`, network, network).Scan(&h)
	return h, err
}

// Database returns the shared SQLite connection for the node registry.
func Database(s *Store) *sql.DB { return s.db }

func ConsumerHandler(s *Store, consumers map[string]string) (http.Handler, error) {
	return feed.Handler(s, consumers, func(network string) bool {
		if s.configured == nil {
			return true
		}
		_, ok := s.configured[network]
		return ok
	})
}
func ConfigureNetworks(s *Store, networks map[string]chains.Network, resolve func(context.Context, string) (int64, error)) {
	s.configured = networks
	s.resolve = resolve
}
