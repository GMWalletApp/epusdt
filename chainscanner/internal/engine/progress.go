package engine

import (
	"context"
	"sort"
	"time"
)

const lagWarning = 5 * time.Minute
const sampleInterval = 5 * time.Second
const maxSamples = 361 // About 30 minutes at the highest sampling frequency.

type observedTarget struct {
	height int64
	at     time.Time
}

type headHistory struct {
	last    observedTarget
	samples []observedTarget
}

func (h *headHistory) record(height int64, at time.Time) {
	if height < 0 || !at.After(h.last.at) {
		return
	}
	h.last = observedTarget{height, at}
	// Discard targets above a revised head. A lower target can still have been
	// outstanding since before the revision.
	for end := len(h.samples); end > 0 && h.samples[end-1].height > height; end-- {
		h.samples[end-1].height = height
		if end == 1 || h.samples[end-2].height < height {
			break
		}
		h.samples = h.samples[:end-1]
	}
	if n := len(h.samples); n > 0 && (h.samples[n-1].height == height || at.Sub(h.samples[n-1].at) < sampleInterval) {
		return
	}
	if len(h.samples) == maxSamples {
		h.samples = h.samples[1:]
	}
	h.samples = append(h.samples, h.last)
}

// RecordScanHead consumes an observation already made by the active scanner.
func (s *Store) RecordScanHead(ctx context.Context, network string, target int64, at time.Time) {
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	p := s.periods[network]
	if p == nil || p.active == nil || ctx.Err() != nil || at.Before(p.started) {
		return
	}
	if drain, ok := s.drains[network]; ok && !drain.open {
		target = min(target, drain.end)
	}
	p.heads.record(target, at)
}

type ScanProgress struct {
	Network string `json:"network"`
	Status  string `json:"status"`
	Target  *int64 `json:"target_height"`
	Cursor  *int64 `json:"cursor_height"`
	LagMS   *int64 `json:"lag_ms"`
}

func (h *headHistory) progress(network string, cursor int64, now time.Time, staleAfter time.Duration) ScanProgress {
	r := ScanProgress{Network: network, Status: "warming"}
	if cursor >= 0 {
		r.Cursor = &cursor
	}
	if h.last.at.IsZero() {
		return r
	}
	target := h.last.height
	r.Target = &target
	if now.Sub(h.last.at) > staleAfter {
		r.Status = "stale"
		return r
	}
	if cursor < 0 {
		return r
	}
	var age time.Duration
	if cursor < target {
		at := h.last.at
		// Find the oldest sampled target still ahead of the contiguous cursor.
		i := sort.Search(len(h.samples), func(i int) bool { return h.samples[i].height > cursor })
		if i < len(h.samples) {
			at = h.samples[i].at
		}
		age = max(0, now.Sub(at))
	}
	ms := age.Milliseconds()
	r.LagMS = &ms
	r.Status = "ok"
	if age > lagWarning {
		r.Status = "warning"
	}
	return r
}

func (s *Store) ScanProgress(ctx context.Context) ([]ScanProgress, error) {
	s.demandMu.Lock()
	defer s.demandMu.Unlock()
	rows := make([]ScanProgress, 0, len(s.configured))
	now := time.Now()
	for network, cfg := range s.configured {
		row := ScanProgress{Network: network, Status: "idle"}
		if p := s.periods[network]; p != nil && p.active != nil {
			cursor, err := s.Height(ctx, network)
			if err != nil {
				return nil, err
			}
			staleAfter := max(lagWarning, 2*time.Duration(cfg.IntervalSeconds)*time.Second)
			row = p.heads.progress(network, cursor, now, staleAfter)
			if row.Status == "warming" && p.heads.last.at.IsZero() && now.Sub(p.started) > staleAfter {
				row.Status = "stale"
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Network < rows[j].Network })
	return rows, nil
}
