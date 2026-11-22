package nodes

import (
	"maps"
	"net/http"
	"slices"
	"sync"
)

type scanAttempts struct {
	mu      sync.Mutex
	total   uint64
	retries uint64
}

func (s *scanAttempts) record(retry bool) {
	if s == nil {
		return // standalone pools have no registry metrics
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total++
	if retry {
		s.retries++
	}
}

type scanRetryStats struct {
	Network       string   `json:"network"`
	TotalAttempts uint64   `json:"total_attempts"`
	RetryAttempts uint64   `json:"retry_attempts"`
	RetryRate     *float64 `json:"retry_rate"`
}

func (s *scanAttempts) snapshot(network string) scanRetryStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := scanRetryStats{Network: network, TotalAttempts: s.total, RetryAttempts: s.retries}
	if s.total > 0 {
		rate := float64(s.retries) / float64(s.total)
		stats.RetryRate = &rate
	}
	return stats
}

func (r *NodeRegistry) scanRetryRate(w http.ResponseWriter, req *http.Request) {
	networks := slices.Sorted(maps.Keys(r.scanAttempts))
	if selected, ok := req.URL.Query()["network"]; ok {
		if len(selected) != 1 || r.scanAttempts[selected[0]] == nil {
			http.Error(w, "expected one configured network", http.StatusBadRequest)
			return
		}
		networks = selected
	}
	rows := make([]scanRetryStats, 0, len(networks))
	for _, network := range networks {
		rows = append(rows, r.scanAttempts[network].snapshot(network))
	}
	nodeReply(w, http.StatusOK, rows, nil)
}
