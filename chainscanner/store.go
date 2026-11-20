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
	db      *sql.DB
	workers sync.Map
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
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Subscribe(ctx context.Context, consumer string, watches []Watch) error {
	if strings.TrimSpace(consumer) == "" {
		return errors.New("empty consumer")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM watches WHERE consumer=?", consumer); err != nil {
		return err
	}
	for _, w := range watches {
		a, err := Canonical(w.Network, w.Address)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO watches VALUES(?,?,?)", consumer, w.Network, a); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Pending(ctx context.Context, consumer string) (*Delivery, error) {
	var d Delivery
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT id,payload FROM deliveries WHERE consumer=? ORDER BY id LIMIT 1", consumer).Scan(&d.ID, &b)
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
	watchers := map[string][]string{}
	rows, err := tx.QueryContext(ctx, "SELECT address,consumer FROM watches WHERE network=?", network)
	if err != nil {
		return err
	}
	for rows.Next() {
		var a, c string
		if err = rows.Scan(&a, &c); err != nil {
			rows.Close()
			return err
		}
		watchers[a] = append(watchers[a], c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
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
			e.Network = network
			e.Addresses = matched
			b, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO deliveries(consumer,payload) VALUES(?,?)", c, b); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// SeedCursor imports a last processed height only on the first start.
func (s *Store) SeedCursor(ctx context.Context, network string, height int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO cursors(network,height) VALUES(?,?)", network, height)
	return err
}

// Height returns the last fully committed height or -1 before first use.
func (s *Store) Height(ctx context.Context, network string) (int64, error) {
	h, ok, e := s.cursor(ctx, network)
	if !ok {
		return -1, e
	}
	return h, e
}
