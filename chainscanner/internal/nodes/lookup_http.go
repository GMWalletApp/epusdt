package nodes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

func (r *NodeRegistry) transactionHandler(configured map[string]chains.Network) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/transactions/{network}/{hash}", r.lookupHandler(configured, false))
	mux.HandleFunc("GET /v1/transactions/{network}/{hash}/strict", r.lookupHandler(configured, true))
	return mux
}

func (r *NodeRegistry) lookupHandler(configured map[string]chains.Network, strict bool) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		n := req.PathValue("network")
		if _, ok := configured[n]; !ok {
			http.Error(w, "network is not configured", 400)
			return
		}
		hash, err := chains.TransactionHash(n, req.PathValue("hash"))
		if err != nil {
			http.Error(w, "invalid transaction hash", 400)
			return
		}
		ctx := req.Context()
		count, rounds := 1, 1
		if strict {
			count, rounds = strictGroupSize, 2
		}
		params := req.URL.Query()
		fallbackOnly := params.Get("fallback_only") == "true"
		if params.Has("fallback_only") && (!strict || !fallbackOnly || len(params["fallback_only"]) != 1) {
			http.Error(w, "fallback_only=true requires strict lookup", http.StatusBadRequest)
			return
		}
		single := strict && params.Get("rounds") == "1"
		if strict && req.URL.Query().Has("rounds") && !single {
			http.Error(w, "rounds must be 1 when supplied", 400)
			return
		}
		omitted := map[string]bool{}
		if single {
			rounds = 1
			omitted, err = previousVoters(req.Header.Get("X-ChainScanner-Exclude"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		if strict {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(45 * time.Second))
		}
		used := map[string]bool{}
		lastStatus := http.StatusBadGateway

		for attempt := 1; attempt <= rounds; attempt++ {
			if ctx.Err() != nil {
				return
			}
			if strict {
				w.Header().Set("X-ChainScanner-Strict-Rounds", strconv.Itoa(attempt))
			}
			candidates, err := r.lookupCandidates(ctx, n, strict)
			if err != nil {
				lastStatus = http.StatusServiceUnavailable
				continue
			}
			if fallbackOnly {
				candidates = slices.DeleteFunc(candidates, func(c lookupCandidate) bool { return !c.fallback })
			}
			if len(omitted) > 0 {
				for _, c := range candidates {
					if omitted[voterToken(c.key)] || c.peerID != "" && omitted[voterToken(c.peerID)] {
						used[c.key] = true
					}
				}
			}
			var group []lookupLease
			read := transactionReader(r.lookup.read)
			if strict && n == "ton" {
				group, read = r.tonGroup(ctx, candidates, used, hash)
			} else {
				group = r.lookup.group(ctx, candidates, used, count)
			}
			if len(group) == 0 {
				lastStatus = http.StatusServiceUnavailable
				continue
			}
			if single {
				w.Header().Set("X-ChainScanner-Pair", pairHeader(group))
			}
			v, err := r.lookup.round(ctx, group, hash, read)
			lastStatus = http.StatusBadGateway
			if err == nil && r.groupAllowed(group) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(struct {
					Network string          `json:"network"`
					Hash    string          `json:"hash"`
					Result  json.RawMessage `json:"result"`
				}{n, hash, v})
				return
			}
			if !strict && errors.Is(err, chains.ErrTransactionMissing) {
				http.Error(w, "transaction not found", 404)
				return
			}
		}
		if strict && !single {
			w.Header().Set("X-ChainScanner-Manual-Review", "required")
		}
		if lastStatus == http.StatusServiceUnavailable {
			http.Error(w, errCapacity.Error(), lastStatus)
		} else {
			http.Error(w, errAgreement.Error(), lastStatus)
		}
	}
}

func previousVoters(header string) (map[string]bool, error) {
	if header == "" {
		return nil, nil
	}
	tokens := strings.SplitN(header, ",", 5)
	if len(tokens) > 4 {
		return nil, errors.New("invalid previous pair")
	}
	voters := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		digest, err := hex.DecodeString(token)
		if err != nil || len(digest) != sha256.Size {
			return nil, errors.New("invalid previous pair")
		}
		voters[strings.ToLower(token)] = true
	}
	return voters, nil
}

func pairHeader(group []lookupLease) string {
	var tokens []string
	for _, lease := range group {
		tokens = append(tokens, voterToken(lease.key))
		if lease.peerID != "" {
			tokens = append(tokens, voterToken(lease.peerID))
		}
	}
	return strings.Join(tokens, ",")
}

func voterToken(key string) string { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }

// A rule can change while a provider is reading the transaction.
func (r *NodeRegistry) groupAllowed(group []lookupLease) bool {
	blocked := r.blacklist.hosts()
	for _, node := range group {
		if blocked.matches(node.primaryURL) || blocked.matches(node.cfg.URL) {
			return false
		}
	}
	return true
}
