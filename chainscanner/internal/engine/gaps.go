package engine

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

// Reserve bounded new work and rotate older holes. A crash after reservation
// leaves every uncommitted span eligible for recovery.
func (s *Store) plan(ctx context.Context, network string, high, head int64, limit int) (work []chains.Span, assigned int64, err error) {
	return s.planAvailable(ctx, network, high, head, max(2, limit), nil, 0)
}

func (s *Store) planAvailable(ctx context.Context, network string, high, head int64, limit int, owned []int64, retryDelay time.Duration) (work []chains.Span, assigned int64, err error) {
	if limit <= 0 {
		return nil, high, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, high, err
	}
	defer tx.Rollback()

	retryLimit := limit
	if high < head {
		retryLimit = max(1, limit/2)
	}
	query := "SELECT first,last FROM scan_gaps WHERE network=? AND last<=?"
	args := []any{network, head}
	if len(owned) > 0 {
		query += " AND first NOT IN (?" + strings.Repeat(",?", len(owned)-1) + ")"
		for _, h := range owned {
			args = append(args, h)
		}
	}
	if retryDelay > 0 {
		query += " AND attempted<=?"
		args = append(args, time.Now().Add(-retryDelay).UnixMicro())
	}
	rows, err := tx.QueryContext(ctx, query+" ORDER BY attempted,first LIMIT ?", append(args, retryLimit)...)

	if err != nil {
		return nil, high, err
	}
	for rows.Next() {
		var r chains.Span
		if err = rows.Scan(&r.First, &r.Last); err != nil {
			rows.Close()
			return nil, high, err
		}
		work = append(work, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, high, err
	}
	retries := len(work)
	assigned = high
	for assigned < head && len(work) < limit {
		width := int64(1)
		if network == "aptos" {
			width = min(100, head-assigned)
		}
		r := chains.Span{First: assigned + 1, Last: assigned + width}
		work = append(work, r)
		assigned = r.Last
	}
	if assigned != high {
		// All new spans have the same width except the final partial page.
		width := work[retries].Last - work[retries].First + 1
		if _, err = tx.ExecContext(ctx, `WITH RECURSIVE spans(first) AS
           (SELECT ?1 UNION ALL SELECT first+?2 FROM spans WHERE ?3-first>=?2)
           INSERT INTO scan_gaps(network,first,last)
           SELECT ?4,first,first+min(?2-1,?3-first) FROM spans`, high+1, width, assigned, network); err != nil {
			return nil, high, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE cursors SET height=? WHERE network=?", assigned, network); err != nil {
			return nil, high, err
		}
	}
	if retries > 0 && retries < len(work) {
		ordered := make([]chains.Span, 0, len(work))
		fresh := work[retries:]
		for i := 0; i < max(retries, len(fresh)); i++ {
			if i < retries {
				ordered = append(ordered, work[i])
			}
			if i < len(fresh) {
				ordered = append(ordered, fresh[i])
			}
		}
		work = ordered
	}
	return work, assigned, tx.Commit()
}

// Keep only hashes bordering unfinished work or the high-water mark. When
// neighbors finish in reverse order the later block supplies the expected hash.
func finishGap(ctx context.Context, tx *sql.Tx, network string, r chains.Span, link chains.BlockLink) error {
	var end int64
	err := tx.QueryRowContext(ctx, "DELETE FROM scan_gaps WHERE network=? AND first=? AND last>=? RETURNING last", network, r.First, r.Last).Scan(&end)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("range is no longer pending")
	}
	if err != nil {
		return err
	}
	if end > r.Last {
		if _, err = tx.ExecContext(ctx, "INSERT INTO scan_gaps(network,first,last) VALUES(?,?,?)", network, r.Last+1, end); err != nil {
			return err
		}
	}
	if link.Hash == "" {
		return nil
	}
	for _, edge := range []struct {
		h    int64
		hash string
	}{{r.First - 1, link.Parent}, {r.Last, link.Hash}} {
		if edge.hash == "" {
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO scan_edges(network,height,hash) VALUES(?,?,?)
            ON CONFLICT(network,height) DO UPDATE SET hash=excluded.hash WHERE hash=excluded.hash`, network, edge.h, edge.hash)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return chains.ErrFork
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM scan_edges WHERE network=?1 AND height=?2
            AND NOT EXISTS(SELECT 1 FROM cursors WHERE network=?1 AND height=?2)
            AND NOT EXISTS(SELECT 1 FROM scan_gaps WHERE network=?1 AND first=?2+1)
            AND NOT EXISTS(SELECT 1 FROM scan_gaps WHERE network=?1 AND last=?2)`, network, edge.h)
		if err != nil {
			return err
		}
	}
	return nil
}

// A failed Aptos page is bisected so that one missing ledger version cannot
// hold up the rest of the page. Single-height holes rotate behind other work.
func (s *Store) deferGap(ctx context.Context, network string, r chains.Span) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	mid := r.First + (r.Last-r.First)/2
	now := time.Now().UnixMicro()
	res, err := tx.ExecContext(ctx, "UPDATE scan_gaps SET last=?,attempted=? WHERE network=? AND first=? AND last=?", mid, now, network, r.First, r.Last)
	if err != nil {
		return err
	}
	size, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if size != 1 {
		return errors.New("range is no longer pending")
	}
	if mid < r.Last {
		if _, err = tx.ExecContext(ctx, "INSERT INTO scan_gaps(network,first,last,attempted) VALUES(?,?,?,?)", network, mid+1, r.Last, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
