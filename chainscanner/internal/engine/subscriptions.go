package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/feed"
)

type drainState struct {
	end     int64
	open    bool
	retired bool // intervals removed in this transaction
}

// settle retires closed intervals only once the contiguous cursor reaches their
// inclusive end. The caller commits this together with any final deliveries.
func settle(ctx context.Context, tx *sql.Tx, network string) (drainState, error) {
	var state drainState
	deleted, err := tx.ExecContext(ctx, `DELETE FROM closing_watches WHERE network=?1 AND to_height<=
 COALESCE((SELECT min(first)-1 FROM scan_gaps WHERE network=?1),(SELECT height FROM cursors WHERE network=?1),-1)`, network)
	if err != nil {
		return state, err
	}
	count, err := deleted.RowsAffected()
	if err != nil {
		return state, err
	}
	state.retired = count > 0
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(to_height),-1),EXISTS(SELECT 1 FROM watches WHERE network=?1) FROM closing_watches WHERE network=?1`, network).Scan(&state.end, &state.open)
	if err == nil && state.end < 0 && !state.open {
		_, err = tx.ExecContext(ctx, "DELETE FROM cursors WHERE network=?", network)
	}
	return state, err
}

func (s *Store) applyState(n string, state drainState) {
	if state.end < 0 {
		delete(s.drains, n)
	} else {
		s.drains[n] = state
	}
	if state.retired {
		s.indexes.Delete(n)
	}
	s.setDemandLocked(n, state.open || state.end >= 0)
}

// Draining reports whether only retiring subscriptions keep the worker active.
func (s *Store) Draining(n string) bool {
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	state, ok := s.drains[n]
	return ok && !state.open
}

// Subscribe replaces one consumer's watch set. Serialize replacements for that
// consumer while allowing scans and other consumers to proceed during RPC.
func (s *Store) Subscribe(ctx context.Context, consumer string, watches []feed.Watch) (feed.SubscriptionReceipt, error) {
	var result feed.SubscriptionReceipt
	if strings.TrimSpace(consumer) == "" {
		return result, errors.New("empty consumer")
	}
	wanted := map[feed.Watch]bool{}
	var ordered []feed.Watch
	for _, w := range watches {
		a, err := chains.Canonical(w.Network, w.Address)
		if err != nil {
			return result, err
		}
		if s.configured != nil {
			if _, ok := s.configured[w.Network]; !ok {
				return result, ErrNetworkNotConfigured
			}
		}
		w.Address = a
		if !wanted[w] {
			ordered = append(ordered, w)
			wanted[w] = true
		}
	}
	lock, _ := s.replacements.LoadOrStore(consumer, new(sync.Mutex))
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	old, removed, tips, err := s.subscriptionChanges(ctx, consumer, ordered, wanted)
	if err != nil {
		return result, err
	}
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	result = feed.SubscriptionReceipt{Registrations: []feed.Registration{}, Terminations: []feed.Termination{}}
	touched := map[string]drainState{}
	for _, reg := range removed {
		var assigned int64
		if err = tx.QueryRowContext(ctx, "SELECT height FROM cursors WHERE network=?", reg.Network).Scan(&assigned); err != nil {
			return result, err
		}
		end := max(assigned, tips[reg.Network].height)
		if _, err = tx.ExecContext(ctx, "DELETE FROM watches WHERE consumer=? AND network=? AND address=?", consumer, reg.Network, reg.Address); err != nil {
			return result, err
		}
		if end >= reg.FromHeight {
			if _, err = tx.ExecContext(ctx, "INSERT INTO closing_watches VALUES(?,?,?,?,?)", consumer, reg.Network, reg.Address, reg.FromHeight, end); err != nil {
				return result, err
			}
		}
		result.Terminations = append(result.Terminations, feed.Termination{Registration: reg, ToHeight: end})
		touched[reg.Network] = drainState{}
	}
	for _, w := range ordered {
		start, exists := old[w]
		if !exists {
			start, err = s.openWatch(ctx, tx, consumer, w, tips[w.Network])
			if err != nil {
				return result, err
			}
			touched[w.Network] = drainState{}
		}
		result.Registrations = append(result.Registrations, feed.Registration{Watch: w, FromHeight: start})
	}
	if err = saveReceipt(ctx, tx, consumer, len(touched) > 0, &result); err != nil {
		return result, err
	}
	for n := range touched {
		state, err := settle(ctx, tx, n)
		if err != nil {
			return result, err
		}
		touched[n] = state
	}
	if err = markCompleted(ctx, tx, consumer, result.Terminations); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	for n, state := range touched {
		s.indexes.Delete(n)
		s.applyState(n, state)
	}
	return result, nil
}

func (s *Store) subscriptionChanges(ctx context.Context, consumer string, ordered []feed.Watch, wanted map[feed.Watch]bool) (map[feed.Watch]int64, []feed.Registration, map[string]boundary, error) {
	old := map[feed.Watch]int64{}
	rows, err := s.db.QueryContext(ctx, "SELECT network,address,from_height FROM watches WHERE consumer=? ORDER BY network,address", consumer)
	if err != nil {
		return nil, nil, nil, err
	}
	var removed []feed.Registration
	need := map[string]bool{}
	for rows.Next() {
		var w feed.Watch
		var h int64
		if err = rows.Scan(&w.Network, &w.Address, &h); err != nil {
			break
		}
		// Retired networks have no live head at which a removal could drain.
		if !chains.Supported(w.Network) {
			continue
		}
		old[w] = h
		if !wanted[w] {
			removed = append(removed, feed.Registration{Watch: w, FromHeight: h})
			need[w.Network] = true
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	checked := map[string]bool{}
	for _, w := range ordered {
		if _, ok := old[w]; ok {
			continue
		}
		if checked[w.Network] || need[w.Network] {
			continue
		}
		checked[w.Network] = true
		if _, exists, err := s.cursor(ctx, w.Network); err != nil {
			return nil, nil, nil, err
		} else if !exists {
			need[w.Network] = true
		}
	}
	// Raw tips bound removals; lookback applies only when opening a cold chain.
	tips := map[string]boundary{}
	for n := range need {
		if s.resolve == nil {
			return nil, nil, nil, feed.ErrCursorNotReady
		}
		_, epoch := s.period(n)
		tip, err := s.resolve(ctx, n)
		if err != nil || tip < 0 {
			return nil, nil, nil, fmt.Errorf("%s: %w", n, feed.ErrCursorNotReady)
		}
		tips[n] = boundary{epoch, tip}
	}
	return old, removed, tips, nil
}

func (s *Store) openWatch(ctx context.Context, tx *sql.Tx, consumer string, w feed.Watch, b boundary) (int64, error) {
	var start int64
	var high int64
	err := tx.QueryRowContext(ctx, "SELECT height FROM cursors WHERE network=?", w.Network).Scan(&high)
	if errors.Is(err, sql.ErrNoRows) {
		if b.period == nil || s.periodLocked(w.Network).changed != b.period {
			return 0, feed.ErrCursorNotReady
		}
		high = ActivationCursor(b.height, s.configured[w.Network])
		if _, err = tx.ExecContext(ctx, "INSERT INTO cursors VALUES(?,?)", w.Network, high); err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}
	// A re-subscription during drain starts after its previous interval.
	err = tx.QueryRowContext(ctx, "SELECT max(?,COALESCE(max(to_height),-1))+1 FROM closing_watches WHERE consumer=? AND network=? AND address=?", high, consumer, w.Network, w.Address).Scan(&start)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO watches(consumer,network,address,from_height) VALUES(?,?,?,?)", consumer, w.Network, w.Address, start); err != nil {
		return 0, err
	}
	return start, nil
}

func saveReceipt(ctx context.Context, tx *sql.Tx, consumer string, changed bool, result *feed.SubscriptionReceipt) error {
	var err error
	if changed {
		data, err := json.Marshal(result.Terminations)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO subscription_receipts VALUES(?,?) ON CONFLICT(consumer) DO UPDATE SET terminations=excluded.terminations", consumer, data); err != nil {
			return err
		}
	} else {
		var data []byte
		err = tx.QueryRowContext(ctx, "SELECT terminations FROM subscription_receipts WHERE consumer=?", consumer).Scan(&data)
		if err == nil {
			if err = json.Unmarshal(data, &result.Terminations); err != nil {
				return err
			}
			result.Terminations = slices.DeleteFunc(result.Terminations, func(t feed.Termination) bool {
				return !chains.Supported(t.Network)
			})
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func markCompleted(ctx context.Context, tx *sql.Tx, consumer string, ends []feed.Termination) error {
	if len(ends) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT network,address,min(from_height) FROM closing_watches WHERE consumer=? GROUP BY network,address", consumer)
	if err != nil {
		return err
	}
	defer rows.Close()
	earliest := map[feed.Watch]int64{}
	for rows.Next() {
		var r feed.Registration
		if err = rows.Scan(&r.Network, &r.Address, &r.FromHeight); err != nil {
			return err
		}
		earliest[r.Watch] = r.FromHeight
	}
	for i := range ends {
		first, pending := earliest[ends[i].Watch]
		ends[i].Completed = !pending || first > ends[i].ToHeight
	}
	return rows.Err()
}
