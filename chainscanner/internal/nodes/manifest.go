package nodes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

// Compare canonical endpoints and settings locally. The preview exchanges
// fingerprints, including for URLs containing project IDs, rather than keys.
type MigrationManifest struct {
	StoreID string            `json:"store_id"`
	Entries map[string]string `json:"entries"`
}

func (m MigrationManifest) digest(v any) string {
	data, _ := json.Marshal(v)
	h := hmac.New(sha256.New, []byte(m.StoreID))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
func (m MigrationManifest) Key(e NodeEndpoint) string { return m.digest(e.Identity()) }
func (m MigrationManifest) Settings(role, purpose string, enabled bool, weight int, key string) string {
	return m.digest([]any{role, purpose, enabled, weight, key})
}
func (r *NodeRegistry) migrationManifest(ctx context.Context) (MigrationManifest, error) {
	m := MigrationManifest{Entries: map[string]string{}}
	if err := r.db.QueryRowContext(ctx, "SELECT value FROM registry_identity WHERE id=1").Scan(&m.StoreID); err != nil {
		return m, err
	}
	rows, err := r.List(ctx, "")
	if err != nil {
		return m, err
	}
	for _, n := range rows {
		cfg := r.network(n, chains.Network{})
		m.Entries[m.Key(n.NodeEndpoint)] = m.Settings(n.Role, n.Purpose, n.Enabled, n.Weight, cfg.APIKey)
	}
	hidden, err := r.db.QueryContext(ctx, "SELECT network,protocol,url FROM rpc_nodes")
	if err != nil {
		return m, err
	}
	defer hidden.Close()
	for hidden.Next() {
		var e NodeEndpoint
		if err := hidden.Scan(&e.Network, &e.Protocol, &e.URL); err != nil {
			return m, err
		}
		key := m.Key(e)
		if _, ok := m.Entries[key]; !ok {
			m.Entries[key] = "unavailable"
		}
	}
	return m, hidden.Err()
}
