package scanneradmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
)

var transport = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func request(ctx context.Context, conn config.ScannerConnection, method, path string, in, out any) (int, error) {
	endpoint, err := conn.Endpoint()
	if err != nil {
		return 0, err
	}
	credential, err := conn.Token(true)
	if err != nil {
		return 0, err
	}
	var body bytes.Buffer
	if in != nil {
		if err = json.NewEncoder(&body).Encode(in); err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint+"/v1/admin/"+path, &body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	res, err := transport.Do(req)
	if err != nil {
		return 0, errors.New("ChainScanner unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return res.StatusCode, fmt.Errorf("ChainScanner node management HTTP %d", res.StatusCode)
	}
	if out != nil {
		err = json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(out)
	}
	return res.StatusCode, err
}

// Progress reads the snapshot API shared by embedded and external scanners.
// Keep each row intact so newer scanner fields reach the dashboard immediately.
func Progress(ctx context.Context, c config.ScannerConnection) (json.RawMessage, error) {
	var rows []json.RawMessage
	if _, err := request(ctx, c, http.MethodGet, "progress", nil, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, errors.New("invalid ChainScanner progress snapshot")
	}
	return json.Marshal(rows) // One SSE data line, including for pretty-printed snapshots.
}

func Inventory(ctx context.Context, c config.ScannerConnection, network, use string) (scanner.RouteInventory, error) {
	var nodes scanner.RouteInventory
	_, err := request(ctx, c, "GET", "routes?"+url.Values{"network": {network}, "use": {use}}.Encode(), nil, &nodes)
	if err == nil && nodes.StoreID == "" {
		err = errors.New("ChainScanner node management upgrade required")
	}
	return nodes, err
}

// CandidateInventory supplies the node routes used by supplementary payment checks.
var CandidateInventory = Inventory

func Manifest(ctx context.Context, c config.ScannerConnection) (scanner.MigrationManifest, error) {
	var m scanner.MigrationManifest
	_, err := request(ctx, c, "GET", "migration-manifest", nil, &m)
	if err == nil && (m.StoreID == "" || m.Entries == nil) {
		err = errors.New("ChainScanner node management upgrade required")
	}
	return m, err
}

func Import(ctx context.Context, c config.ScannerConnection, storeID string, node scanner.NodeOptions) (int, error) {
	status, err := request(ctx, c, "POST", "nodes/import?store_id="+url.QueryEscape(storeID), node, nil)
	return status, err
}

type ConnectionInfo struct {
	scanner.ManagementCapabilities
	Embedded bool   `json:"embedded"`
	Target   string `json:"target,omitempty"`
}

func Connection(ctx context.Context, c config.ScannerConnection) (ConnectionInfo, error) {
	info := ConnectionInfo{Embedded: c.Embedded}
	if !c.Embedded {
		var err error
		info.Target, err = c.Endpoint()
		if err != nil {
			return info, err
		}
	}
	status, err := request(ctx, c, "GET", "capabilities", nil, &info.ManagementCapabilities)
	if status == http.StatusNotFound {
		// Older registries expose the base node API without advertising capabilities.
		var rows []scanner.Node
		_, err = request(ctx, c, "GET", "nodes?network=base", nil, &rows)
		if err == nil && rows == nil {
			return info, errors.New("invalid ChainScanner inventory")
		}
		info.ManagementCapabilities = scanner.ManagementCapabilities{Protocols: scanner.ManagementProtocols()}
	}
	if err == nil && len(info.Protocols) == 0 {
		err = errors.New("invalid ChainScanner capabilities")
	}
	return info, err
}
