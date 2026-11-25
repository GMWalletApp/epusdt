// Package diagnostics keeps short-lived support events separate from payment storage.
package diagnostics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const retention = 24 * time.Hour

var active atomic.Pointer[Store]
var ErrUnavailable = errors.New("diagnostics unavailable")

// Event has no fields for request bodies, credentials, raw errors or URLs.
type Event struct {
	Time          time.Time `json:"time"`
	Event         string    `json:"event"`
	Outcome       string    `json:"outcome,omitempty"`
	Trade         string    `json:"trade_id,omitempty"`
	Parent        string    `json:"parent_trade_id,omitempty"`
	MerchantOrder string    `json:"order_id,omitempty"`
	Order         uint64    `json:"order_row_id,omitempty"`
	Hash          string    `json:"tx_hash,omitempty"`
	Network       string    `json:"network,omitempty"`
	Token         string    `json:"token,omitempty"`
	Amount        float64   `json:"actual_amount,omitempty"`
	Status        int       `json:"order_status,omitempty"`
	Height        int64     `json:"block_height,omitempty"`
	Cursor        *int64    `json:"cursor_height,omitempty"`
	Target        *int64    `json:"target_height,omitempty"`
	ScanFailures  uint64    `json:"scan_failure_count,omitempty"`
	NodeHead      *int64    `json:"node_height,omitempty"`
	Confirmations int64     `json:"confirmations,omitempty"`
	Required      int64     `json:"required_confirmations,omitempty"`
	Node          int64     `json:"node_id,omitempty"`
	Protocol      string    `json:"protocol,omitempty"`
	Role          string    `json:"role,omitempty"`
	Source        string    `json:"scan_source,omitempty"`
	Mode          string    `json:"mode,omitempty"`
	Attempt       int       `json:"attempt,omitempty"`
	HTTP          int       `json:"http_status,omitempty"`
	Code          int       `json:"error_code,omitempty"`
	Latency       int64     `json:"latency_ms,omitempty"`
	RetryAt       int64     `json:"retry_at_ms,omitempty"`
	Count         uint64    `json:"count,omitempty"`
	Version       string    `json:"version,omitempty"`
}

type Store struct {
	db      *sql.DB
	queue   chan Event
	dropped atomic.Uint64
	enabled func() bool
}

func Start(ctx context.Context, dir string, enabled func() bool) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "events.sqlite")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute), RawQuery: "_pragma=busy_timeout(2000)&_pragma=secure_delete(ON)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(3)
	const schema = `CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, trade TEXT NOT NULL, parent TEXT NOT NULL, merchant TEXT NOT NULL, hash TEXT NOT NULL, payload TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS events_at ON events(at);
CREATE INDEX IF NOT EXISTS events_trade ON events(trade);
CREATE INDEX IF NOT EXISTS events_parent ON events(parent);
CREATE INDEX IF NOT EXISTS events_merchant ON events(merchant);
CREATE INDEX IF NOT EXISTS events_hash ON events(hash);`
	s := &Store{db: db, queue: make(chan Event, 2048), enabled: enabled}
	if _, err = db.Exec(schema); err == nil {
		err = s.prune(time.Now())
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	active.Store(s)
	go func() { defer close(done); s.run(ctx) }()
	return func() { active.CompareAndSwap(s, nil); cancel(); <-done; db.Close() }, nil
}

func Enabled() bool { s := active.Load(); return s != nil && s.enabled() }

func Record(e Event) {
	s := active.Load()
	if s == nil || !s.enabled() {
		return
	}
	e.Time = time.Now().UTC()
	select {
	case s.queue <- e:
	default:
		s.dropped.Add(1)
	}
}

func (s *Store) prune(now time.Time) error {
	_, err := s.db.Exec("DELETE FROM events WHERE at <= ?", now.Add(-retention).UnixMilli())
	return err
}

func (s *Store) write(first Event) error {
	batch := []Event{first}
drain:
	for len(batch) < 128 {
		select {
		case e := <-s.queue:
			batch = append(batch, e)
		default:
			break drain
		}
	}
	if n := s.dropped.Swap(0); n > 0 {
		batch = append(batch, Event{Time: time.Now().UTC(), Event: "diagnostics.dropped", Count: n})
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT INTO events(at,trade,parent,merchant,hash,payload) VALUES(?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range batch {
		payload, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err = stmt.Exec(e.Time.UnixMilli(), e.Trade, e.Parent, e.MerchantOrder, e.Hash, string(payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var lastWarning time.Time
	report := func(err error) {
		if err != nil && time.Since(lastWarning) > time.Minute {
			log.Print("support diagnostics storage operation failed")
			lastWarning = time.Now()
		}
	}
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case e := <-s.queue:
					report(s.write(e))
				default:
					return
				}
			}
		case now := <-tick.C:
			report(s.prune(now))
		case e := <-s.queue:
			report(s.write(e))
		}
	}
}

// Export streams short pages. A slow download never holds a database cursor
// across network writes or causes freshly arriving events to extend the export.
func Export(ctx context.Context, w io.Writer, match string) error {
	s := active.Load()
	if s == nil {
		return ErrUnavailable
	}
	cutoff := time.Now().Add(-retention).UnixMilli()
	var last, maxID int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(max(id),0) FROM events").Scan(&maxID); err != nil {
		return err
	}
	for last < maxID {
		query := "SELECT id,payload FROM events WHERE id>?1 AND id<=?2 AND at>?3"
		args := []any{last, maxID, cutoff}
		if match != "" {
			// Include the scan and retry events that only know the transaction
			// hash, plus order events written before that hash was available.
			query = `WITH linked AS MATERIALIZED (
 SELECT DISTINCT trade,parent,merchant,hash FROM events
 WHERE at>?3 AND id<=?2 AND (trade=?4 OR parent=?4 OR merchant=?4 OR hash=?4)
), trades AS (SELECT trade FROM linked WHERE trade<>'' UNION SELECT parent FROM linked WHERE parent<>'')
` + query + ` AND (trade IN trades OR parent IN trades OR
merchant IN (SELECT merchant FROM linked WHERE merchant<>'') OR
hash IN (SELECT hash FROM linked WHERE hash<>''))`
			args = append(args, match)
		}
		rows, err := s.db.QueryContext(ctx, query+" ORDER BY id LIMIT 128", args...)
		if err != nil {
			return err
		}
		var page []string
		for rows.Next() {
			var line string
			if err = rows.Scan(&last, &line); err != nil {
				rows.Close()
				return err
			}
			page = append(page, line)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, line := range page {
			if _, err = fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		if len(page) < 128 {
			break
		}
	}
	return nil
}
