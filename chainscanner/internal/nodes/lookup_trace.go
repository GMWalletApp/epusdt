package nodes

import (
	"context"
	"errors"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

// LookupAttempt contains only non-secret fields for an authenticated consumer
// requesting a diagnostic trace. IDs refer to the scanner's node registry.
type LookupAttempt struct {
	NodeID    int64  `json:"node_id"`
	Fallback  bool   `json:"fallback,omitempty"`
	Reused    bool   `json:"reused,omitempty"`
	Head      *int64 `json:"head,omitempty"`
	Result    string `json:"result"`
	LatencyMS int64  `json:"latency_ms"`
}

func lookupOutcome(err error) string {
	switch {
	case err == nil:
		return "found"
	case errors.Is(err, chains.ErrTransactionMissing):
		return "missing"
	case errors.Is(err, chains.ErrTransactionPending):
		return "pending"
	case errors.Is(err, chains.ErrTransactionData):
		return "incomplete"
	case errors.Is(err, chains.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errCapacity):
		return "unavailable"
	default:
		return "read_error"
	}
}
