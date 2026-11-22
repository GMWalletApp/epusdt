package nodes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/engine"
)

func TestScanRetryRateAPI(t *testing.T) {
	store, err := engine.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry, err := NewNodeRegistry(t.Context(), store, []chains.Network{{Name: "solana"}, {Name: "base"}})
	if err != nil {
		t.Fatal(err)
	}
	const admin = "retry-statistics-admin-token"
	const consumer = "retry-statistics-consumer-token"
	handler, err := registry.Handler(map[string]string{"test": consumer}, admin)
	if err != nil {
		t.Fatal(err)
	}
	query := func(filter, token string, status int) []scanRetryStats {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/scan-retry-rate"+filter, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("GET %q: got %d: %s", filter, w.Code, w.Body.String())
		}
		var rows []scanRetryStats
		if status == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
		}
		return rows
	}
	query("", consumer, 401)
	query("?network=ethereum", admin, 400)
	query("?network=", admin, 400)
	query("?network=base&network=solana", admin, 400)
	registry.scanAttempts["base"].record(false)
	registry.scanAttempts["base"].record(false)
	registry.scanAttempts["base"].record(true)
	rows := query("", admin, 200)
	if len(rows) != 2 || rows[0].Network != "base" || rows[1].Network != "solana" || rows[1].TotalAttempts != 0 || rows[1].RetryRate != nil {
		t.Fatalf("invalid network listing: %+v", rows)
	}
	rows = query("?network=base", admin, 200)
	if len(rows) != 1 || rows[0].TotalAttempts != 3 || rows[0].RetryAttempts != 1 || rows[0].RetryRate == nil || *rows[0].RetryRate != 1.0/3 {
		t.Fatalf("invalid retry stats: %+v", rows)
	}
}
