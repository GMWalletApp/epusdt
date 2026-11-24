package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

func (r *NodeRegistry) transactionHandler(configured map[string]chains.Network) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/transactions/{network}/{hash}", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		network := req.PathValue("network")
		if _, ok := configured[network]; !ok {
			http.Error(w, "network is not configured", 400)
			return
		}
		hash, err := chains.TransactionHash(network, req.PathValue("hash"))
		if err != nil {
			http.Error(w, "invalid transaction hash", 400)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 38*time.Second)
		defer cancel()
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(45 * time.Second))
		q := req.URL.Query()
		sources, err := r.scanSources(ctx, network, hash)
		if err != nil || q.Get("observed") == "true" && len(sources.all) == 0 {
			http.Error(w, "scan source unavailable", 503)
			return
		}
		if q.Has("observed_height") {
			height, err := strconv.ParseInt(q.Get("observed_height"), 10, 64)
			if err != nil || height < 0 || q.Get("observed") != "true" {
				http.Error(w, "invalid observed_height", 400)
				return
			}
			sources.height = max(sources.height, height)
		}
		candidates, err := r.lookupCandidates(ctx, network, q.Get("manual") == "true")
		if err != nil {
			http.Error(w, errCapacity.Error(), 503)
			return
		}
		eligible := candidates[:0]
		for _, c := range candidates {
			if q.Get("fallback_only") == "true" && !c.fallback {
				continue
			}
			c.sourceStatus = "unobserved"
			if len(sources.all) > 0 {
				c.sourceStatus = "excluded"
				if chains.SharesScanSource(c.cfg, sources.all) {
					if !c.fallback || chains.SharesScanSource(c.cfg, sources.regular) {
						continue
					}
					c.sourceStatus = "fallback_reused"
				}
			}
			eligible = append(eligible, c)
		}
		r.lookup.prioritize(eligible)
		raw, source, err := r.lookupHash(ctx, eligible, hash, sources.height)
		if source != "" {
			w.Header().Set("X-ChainScanner-Scan-Source", source)
		}
		switch {
		case err == nil:
			writeTransactionResult(w, network, hash, raw)
		case errors.Is(err, chains.ErrTransactionMissing):
			w.Header().Set("X-ChainScanner-Evidence", "not_found")
			if source != "unobserved" {
				w.Header().Set("X-ChainScanner-Height-Checked", "true")
			}
			http.Error(w, "transaction not found", 404)
		case errors.Is(err, errCapacity):
			http.Error(w, errCapacity.Error(), 503)
		default:
			if reason := lookupEvidenceReason(err); reason != "" {
				w.Header().Set("X-ChainScanner-Evidence", reason)
			}
			http.Error(w, errLookup.Error(), 502)
		}
	})
	return mux
}

type scanSources struct {
	all, regular map[string]bool
	height       int64
}

func (r *NodeRegistry) scanSources(ctx context.Context, network, hash string) (scanSources, error) {
	sources := scanSources{all: map[string]bool{}, regular: map[string]bool{}, height: -1}
	rows, err := r.db.QueryContext(ctx, "SELECT source,height,fallback FROM transaction_sources WHERE network=? AND hash=?", network, hash)
	if err != nil {
		return sources, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var height int64
		var fallback bool
		if err := rows.Scan(&key, &height, &fallback); err != nil {
			return sources, err
		}
		sources.height = max(sources.height, height)
		sources.all[key] = true
		if !fallback {
			sources.regular[key] = true
		}
	}
	return sources, rows.Err()
}

// Try each available provider before reusing one. With a small pool rotate
// through alternatives until the initial read and three retries have run.
func (r *NodeRegistry) lookupHash(ctx context.Context, candidates []lookupCandidate, hash string, height int64) (json.RawMessage, string, error) {
	attempts := 0
	lastKey := ""
	lastErr := errCapacity
	pending := false
	missingSource := ""
	qualifiedMissing := make(map[string]bool)
	for ctx.Err() == nil {
		before := attempts
		seen := map[string]bool{}
		for _, c := range candidates {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			if qualifiedMissing[c.key] || seen[c.key] || c.key == lastKey || !r.lookup.ready(c.version, time.Now()) {
				continue
			}
			release, ok := r.lookup.lanes.tryLookup(c.key, true)
			if !ok {
				continue
			}
			seen[c.key] = true
			lastKey = c.key
			attempts++
			call, cancel := context.WithTimeout(ctx, 8*time.Second)
			raw, err := r.lookupAtObservedHeight(call, c, candidates, hash, height)
			cancel()
			release()
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			r.lookup.record(c.version, err)
			if !r.lookupAllowed(ctx, c) {
				lastErr = errLookup
				continue
			}
			if err == nil {
				return raw, c.sourceStatus, nil
			}
			if errors.Is(err, chains.ErrTransactionMissing) {
				if c.sourceStatus == "unobserved" && !pending {
					return nil, c.sourceStatus, err
				}
				qualifiedMissing[c.key] = true
				missingSource = c.sourceStatus
				continue
			}
			if errors.Is(err, chains.ErrTransactionPending) {
				pending = true
			}
			lastErr = err
		}
		if attempts == before || attempts >= 4 {
			break
		}
	}
	if pending {
		return nil, "", chains.ErrTransactionPending
	}
	if missingSource != "" {
		return nil, missingSource, chains.ErrTransactionMissing
	}
	return nil, "", lastErr
}

func writeTransactionResult(w http.ResponseWriter, network, hash string, result json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Network string          `json:"network"`
		Hash    string          `json:"hash"`
		Result  json.RawMessage `json:"result"`
	}{network, hash, result})
}

func lookupEvidenceReason(err error) string {
	switch {
	case errors.Is(err, chains.ErrTransactionData):
		return "incomplete"
	case errors.Is(err, chains.ErrTransactionPending):
		return "pending"
	default:
		return ""
	}
}

// A rule can change while a provider is reading the transaction.
func (r *NodeRegistry) lookupAllowed(ctx context.Context, node lookupCandidate) bool {
	current, err := r.get(ctx, node.version.id)
	if err != nil || !current.Enabled || current.version() != node.version {
		return false
	}
	blocked := r.blacklist.hosts()
	return !blocked.matches(node.primaryURL) && !blocked.matches(node.cfg.URL)
}
