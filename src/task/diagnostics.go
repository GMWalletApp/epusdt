package task

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/diagnostics"
	"github.com/GMWalletApp/epusdt/internal/scanneradmin"
)

// Inspect the registry's existing observations. This issues no node probes.
func recordNodeHealth(ctx context.Context, conn config.ScannerConnection) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	previous := map[int64]string{}
	protocols := scanner.ManagementProtocols()
	var snapshot time.Time
	for ctx.Err() == nil {
		if diagnostics.Enabled() {
			recordScanProgress(ctx, conn)
			nodes, err := scanneradmin.NodeStatuses(ctx, conn)
			if err != nil {
				diagnostics.Record(diagnostics.Event{Event: "node.inventory", Outcome: "unavailable"})
			} else {
				full := time.Since(snapshot) >= time.Hour
				if full {
					snapshot = time.Now()
				}
				next := make(map[int64]string, len(nodes))
				for _, n := range nodes {
					if !scanner.Supported(n.Network) {
						continue
					}
					if !slices.Contains(protocols[n.Network], n.Protocol) {
						n.Protocol = "unknown"
					}
					status := n.Status
					if !n.Enabled {
						status = "disabled"
					}
					switch status {
					case "ok", "untested", "timeout", "unavailable", "disabled":
					default:
						status = "unknown"
					}
					role := "regular"
					if n.Role == "fallback" {
						role = "fallback"
					}
					next[n.ID] = status + "/" + role
					if full || next[n.ID] != previous[n.ID] {
						event := diagnostics.Event{Event: "node.health", Node: n.ID, Network: n.Network, Protocol: n.Protocol, Role: role, Outcome: status, NodeHead: n.Height}
						if n.Latency != nil {
							event.Latency = *n.Latency
						}
						diagnostics.Record(event)
					}
				}
				previous = next
			}
		} else {
			snapshot = time.Time{}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Store the scanner's cursor and failure counter even when no payment event arrives.
func recordScanProgress(ctx context.Context, conn config.ScannerConnection) {
	raw, err := scanneradmin.Progress(ctx, conn)
	var rows []struct {
		Network  string `json:"network"`
		Status   string `json:"status"`
		Cursor   *int64 `json:"cursor_height"`
		Target   *int64 `json:"target_height"`
		Failures uint64 `json:"scan_failure_count"`
	}
	if err != nil || json.Unmarshal(raw, &rows) != nil {
		diagnostics.Record(diagnostics.Event{Event: "scan.progress", Outcome: "unavailable"})
		return
	}
	for _, row := range rows {
		if !scanner.Supported(row.Network) {
			continue
		}
		switch row.Status {
		case "idle":
			continue
		case "warming", "stale", "ok", "warning":
		default:
			row.Status = "unknown"
		}
		diagnostics.Record(diagnostics.Event{Event: "scan.progress", Network: row.Network, Outcome: row.Status, Cursor: row.Cursor, Target: row.Target, ScanFailures: row.Failures})
	}
}

func recordLookupAttempts(event diagnostics.Event, raw string) {
	if raw == "" || len(raw) > 8192 {
		return
	}
	var attempts []scanner.LookupAttempt
	if json.Unmarshal([]byte(raw), &attempts) != nil || len(attempts) > 32 {
		return
	}
	event.Event = "node.lookup_attempt"
	for i, a := range attempts {
		event.Attempt = i + 1
		event.Node = a.NodeID
		event.NodeHead = a.Head
		event.Latency = a.LatencyMS
		event.Role = "regular"
		if a.Fallback {
			event.Role = "fallback"
		}
		event.Source = ""
		if a.Reused {
			event.Source = "fallback_reused"
		}
		switch a.Result {
		case "found", "missing", "pending", "incomplete", "timeout", "rate_limited", "unavailable", "read_error", "configuration_changed":
			event.Outcome = a.Result
		default:
			event.Outcome = "unknown"
		}
		diagnostics.Record(event)
	}
}
