package chains

import (
	"context"
	"encoding/json"
)

// Source returns contiguous confirmed heights (ledger versions for Aptos).
type Source interface {
	Head(context.Context) (int64, error)
	Read(ctx context.Context, next, head int64) (end int64, events []Activity, err error)
	Close()
}
type Network struct {
	AssetEvents     *AssetEventRules `json:"asset_events,omitempty"`
	Protocol        string           `json:"protocol,omitempty"`
	HistoryURL      string           `json:"history_url,omitempty"`
	HistoryAPIKey   string           `json:"history_api_key,omitempty"`
	Name            string           `json:"name"`
	URL             string           `json:"url"`
	APIKey          string           `json:"api_key,omitempty"`
	Confirmations   int64            `json:"confirmations,omitempty"`
	IntervalSeconds int              `json:"interval_seconds,omitempty"`
	Lookback        int64            `json:"lookback,omitempty"`
	TraceCalls      bool             `json:"trace_calls,omitempty"`
}

// Activity carries chain evidence, including failed and outgoing transactions.
// A subscriber is responsible for interpreting it.
type Activity struct {
	ScanSources   []string        `json:"-"` // Internal provenance for the committed block read.
	Confirmations int64           `json:"confirmations"`
	Network       string          `json:"network"`
	Height        int64           `json:"height"`
	TxID          string          `json:"tx_id"`
	TimeMS        int64           `json:"time_ms"`
	Addresses     []string        `json:"addresses"`
	Data          json.RawMessage `json:"data"`
}
