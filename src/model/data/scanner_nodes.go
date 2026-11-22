package data

import (
	"context"
	"slices"
	"sync/atomic"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/scanneradmin"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

var runtimeScanner atomic.Pointer[config.ScannerConnection]

func UseScannerNodes(connection config.ScannerConnection) { runtimeScanner.Store(&connection) }

func scannerCandidates(network string, protocols []string, use string) ([]mdb.RpcNode, error) {
	conn := runtimeScanner.Load()
	if conn == nil {
		conn = &config.ScannerConnection{}
	}
	result, err := scanneradmin.CandidateInventory(context.Background(), *conn, network, use)
	if err != nil {
		return nil, err
	}
	rows := make([]mdb.RpcNode, 0, len(result.Nodes))
	for _, n := range result.Nodes {
		protocol := n.Protocol
		if !slices.Contains(protocols, protocol) {
			protocol = n.NodeEndpoint.Identity().Protocol
			if !slices.Contains(protocols, protocol) {
				continue
			}
		}
		status := mdb.RpcNodeStatusUnknown
		if n.Status == "ok" {
			status = mdb.RpcNodeStatusOk
		}
		row := mdb.RpcNode{Network: n.Network, Type: protocol, Url: n.URL, ApiKey: n.APIKey, Purpose: n.Purpose, Weight: n.Weight, Enabled: n.Enabled, Status: status}
		row.ID = uint64(n.ID)
		rows = append(rows, row)
	}
	return rows, nil
}
