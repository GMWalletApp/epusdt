package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
	_ "modernc.org/sqlite"
)

type Store struct {
	transactions http.Handler
	demandMu     sync.Mutex
	replacements sync.Map // consumer -> mutex; independent consumers may resolve boundaries concurrently
	drains       map[string]drainState
	periods      map[string]*demandPeriod
	configured   map[string]chains.Network
	resolve      func(context.Context, string) (int64, error)
	waitMu       sync.Mutex
	waiters      map[queueKey]chan struct{}
	db           *sql.DB
	workers      sync.Map
	indexes      sync.Map // network -> immutable address index, loaded inside the SQLite transaction
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
 CREATE TABLE IF NOT EXISTS watches(consumer TEXT NOT NULL, network TEXT NOT NULL, address TEXT NOT NULL, from_height INTEGER NOT NULL, PRIMARY KEY(consumer,network,address));
 CREATE INDEX IF NOT EXISTS by_address ON watches(network,address);
 CREATE TABLE IF NOT EXISTS closing_watches(consumer TEXT NOT NULL,network TEXT NOT NULL,address TEXT NOT NULL,from_height INTEGER NOT NULL,to_height INTEGER NOT NULL,PRIMARY KEY(network,consumer,address,from_height));
 CREATE TABLE IF NOT EXISTS subscription_receipts(consumer TEXT PRIMARY KEY, terminations BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS cursors(network TEXT PRIMARY KEY,height INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS scan_gaps(network TEXT NOT NULL,first INTEGER NOT NULL,last INTEGER NOT NULL,attempted INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(network,first));
 CREATE INDEX IF NOT EXISTS gap_retry ON scan_gaps(network,attempted,first);
 CREATE INDEX IF NOT EXISTS failed_gap_end ON scan_gaps(network,last) WHERE attempted>0;
 CREATE UNIQUE INDEX IF NOT EXISTS gap_end ON scan_gaps(network,last);
 CREATE TABLE IF NOT EXISTS scan_edges(network TEXT NOT NULL,height INTEGER NOT NULL,hash TEXT NOT NULL,PRIMARY KEY(network,height));
 CREATE TRIGGER IF NOT EXISTS clear_scan_work AFTER DELETE ON cursors BEGIN
 DELETE FROM scan_gaps WHERE network=OLD.network;
 DELETE FROM scan_edges WHERE network=OLD.network;
 END;
 CREATE TABLE IF NOT EXISTS transaction_sources(network TEXT NOT NULL, hash TEXT NOT NULL, source TEXT NOT NULL, PRIMARY KEY(network,hash,source)) WITHOUT ROWID;
 CREATE TABLE IF NOT EXISTS deliveries(id INTEGER PRIMARY KEY AUTOINCREMENT, consumer TEXT NOT NULL, network TEXT NOT NULL, payload BLOB NOT NULL);
 CREATE INDEX IF NOT EXISTS by_network_delivery ON deliveries(consumer,network,id);`)
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
	_, err = db.Exec("DELETE FROM cursors WHERE NOT EXISTS (SELECT 1 FROM watches WHERE watches.network=cursors.network) AND NOT EXISTS (SELECT 1 FROM closing_watches WHERE closing_watches.network=cursors.network)")
	if err != nil {
		db.Close()
		return nil, err
	}
	st := &Store{db: db, drains: map[string]drainState{}, waiters: map[queueKey]chan struct{}{}, periods: map[string]*demandPeriod{}}
	rows, err := db.Query(`WITH networks(network) AS (
 SELECT network FROM watches UNION SELECT network FROM closing_watches
 ) SELECT network, EXISTS(SELECT 1 FROM watches w WHERE w.network=n.network),
 COALESCE((SELECT max(to_height) FROM closing_watches c WHERE c.network=n.network),-1) FROM networks n`)
	if err != nil {
		db.Close()
		return nil, err
	}
	for rows.Next() {
		var name string
		var state drainState
		if err = rows.Scan(&name, &state.open, &state.end); err != nil {
			break
		}
		if chains.Supported(name) {
			st.applyState(name, state)
		}
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
func (s *Store) pending(ctx context.Context, consumer, network string) (*feed.Delivery, error) {
	var d feed.Delivery
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT id,payload FROM deliveries WHERE consumer=? AND network=? ORDER BY id LIMIT 1", consumer, network).Scan(&d.ID, &b)
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

// Caller holds demandMu, so a watch update cannot cross this commit.
func (s *Store) complete(ctx context.Context, network string, span chains.Span, link chains.BlockLink, events []chains.Activity) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = finishGap(ctx, tx, network, span, link); err != nil {
		return err
	}
	notify := map[string]bool{}
	if len(events) > 0 {
		if err = s.enqueue(ctx, tx, network, events, notify); err != nil {
			return err
		}
	}
	var state drainState
	_, draining := s.drains[network]
	if draining {
		state, err = settle(ctx, tx, network)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if draining {
		s.applyState(network, state)
	}
	s.waitMu.Lock()
	for c := range notify {
		key := queueKey{c, network}
		if ch := s.waiters[key]; ch != nil {
			close(ch)
			delete(s.waiters, key)
		}
	}
	s.waitMu.Unlock()
	return nil
}

func (s *Store) enqueue(ctx context.Context, tx *sql.Tx, network string, events []chains.Activity, notify map[string]bool) error {
	watchers, err := s.addressIndex(ctx, tx, network)
	if err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, "INSERT INTO deliveries(consumer,network,payload) VALUES(?,?,?)")
	if err != nil {
		return err
	}
	defer insert.Close()
	for _, e := range events {
		targets := map[string][]string{}
		seen := map[string]bool{}
		for _, a := range e.Addresses {
			if seen[a] {
				continue
			}
			seen[a] = true
			for _, c := range watchers[a] {
				if e.Height >= c.from && (!c.to.Valid || e.Height <= c.to.Int64) {
					targets[c.consumer] = append(targets[c.consumer], a)
				}
			}
		}
		if len(targets) > 0 && len(e.ScanSources) > 0 {
			hash, err := chains.TransactionHash(network, e.TxID)
			if err == nil {
				for _, source := range e.ScanSources {
					if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO transaction_sources(network,hash,source) VALUES(?,?,?)", network, hash, source); err != nil {
						return err
					}
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
	return nil
}

// Access under the store's single SQLite connection orders index invalidation
// with watch mutations and block commits, including rolled-back registrations.
type watcher struct {
	consumer string
	from     int64
	to       sql.NullInt64
}

func (s *Store) addressIndex(ctx context.Context, tx *sql.Tx, network string) (map[string][]watcher, error) {
	if v, ok := s.indexes.Load(network); ok {
		return v.(map[string][]watcher), nil
	}
	watchers := map[string][]watcher{}
	rows, err := tx.QueryContext(ctx, "SELECT address,consumer,from_height,NULL FROM watches WHERE network=?1 UNION ALL SELECT address,consumer,from_height,to_height FROM closing_watches WHERE network=?1", network)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a string
		var c watcher
		if err = rows.Scan(&a, &c.consumer, &c.from, &c.to); err != nil {
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

func SetTransactionHandler(s *Store, h http.Handler) { s.transactions = h }

func ConsumerHandler(s *Store, consumers map[string]string) (http.Handler, error) {
	return feed.Handler(s, consumers, s.transactions, func(network string) bool {
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
