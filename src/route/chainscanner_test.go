package route

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	scanner "github.com/GMWalletApp/ChainScanner"
)

func TestScannerAdminBlacklistAndFallback(t *testing.T) {
	store, err := scanner.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reg, err := scanner.NewNodeRegistry(t.Context(), store, []scanner.Network{{Name: "solana"}})
	if err != nil {
		t.Fatal(err)
	}
	adminToken := strings.Repeat("a", 32)
	consumer := strings.Repeat("c", 32)
	h, err := reg.Handler(map[string]string{"epusdt": consumer}, adminToken)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	t.Setenv("CHAINSCANNER_URL", server.URL)
	t.Setenv("CHAINSCANNER_ADMIN_TOKEN", adminToken)
	t.Setenv("CHAINSCANNER_TOKEN", consumer)
	e, jwt := setupAdminTestEnv(t)
	settingsPath := "/admin/api/v1/settings"
	settings := doGetAdmin(e, settingsPath+"?group=system", jwt)
	if settings.Code != 200 {
		t.Fatal(settings.Body.String())
	}
	for _, enabled := range []bool{false, true} {
		w := doPutAdmin(e, settingsPath, map[string]any{"items": []map[string]any{{"key": "system.strict_payment_verification", "group": "system", "type": "bool", "value": enabled}}}, jwt)
		if !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatal(w.Body.String())
		}
	}
	const base = "/admin/api/v1/chainscanner"
	call := func(method, path, body string, status int) string {
		t.Helper()
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+jwt)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	// No JWT: neither a consumer nor a privileged upstream request is possible.
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest("GET", base+"/admin/nodes", nil))
	if w.Code == 200 {
		t.Fatal("admin route accessible without JWT")
	}
	var progress []struct{ Network, Status string }
	if err := json.Unmarshal([]byte(call("GET", "/admin/progress", "", 200)), &progress); err != nil || len(progress) != 1 || progress[0].Network != "solana" || progress[0].Status != "idle" {
		t.Fatalf("scanner progress: %+v, %v", progress, err)
	}
	const host = "provider.example"
	for i, endpoint := range []string{"/admin/nodes", "/admin/nodes/fallback"} {
		b := call("POST", endpoint, `{"network":"solana","protocol":"http","url":"https://`+[]string{"ordinary.", "fallback."}[i]+host+`"}`, 201)
		var n scanner.Node
		if err := json.Unmarshal([]byte(b), &n); err != nil {
			t.Fatal(err)
		}
	}
	path := "/admin/blacklist/keyword:provider"
	call("PUT", path, "", 204)
	call("PUT", path, "", 204)
	if !strings.Contains(call("GET", "/admin/blacklist", "", 200), "keyword:provider") {
		t.Fatal("missing persisted blacklist rule")
	}
	if strings.Contains(call("GET", "/admin/nodes?network=solana", "", 200), host) {
		t.Fatal("blocked nodes still in inventory")
	}
	call("DELETE", path, "", 204)
	if !strings.Contains(call("GET", "/admin/nodes?network=solana", "", 200), host) {
		t.Fatal("node not restored")
	}
	// Use only the two local RPC servers for ordinary and strict transaction lookups.
	rows, err := reg.List(t.Context(), "solana")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range rows {
		call("DELETE", fmt.Sprintf("/admin/nodes/%d", n.ID), "", 204)
	}
	signature := strings.Repeat("1", 64)
	handleRPC := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     int
			Method string
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		var value any = int64(123)
		if q.Method == "getGenesisHash" {
			value = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
		}
		if q.Method == "getTransaction" {
			value = map[string]any{"slot": 123, "transaction": map[string]any{"signatures": []string{signature}, "message": map[string]any{}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": value})
	})
	rpc := httptest.NewServer(handleRPC)
	defer rpc.Close()
	second := httptest.NewServer(handleRPC)
	defer second.Close()
	call("POST", "/admin/nodes/fallback", fmt.Sprintf(`{"network":"solana","protocol":"http","url":%q}`, second.URL), 201)
	var node scanner.Node
	if err := json.Unmarshal([]byte(call("POST", "/admin/nodes", fmt.Sprintf(`{"network":"solana","protocol":"http","url":%q}`, rpc.URL), 201)), &node); err != nil {
		t.Fatal(err)
	}
	checked := call("POST", fmt.Sprintf("/admin/nodes/%d/test", node.ID), "", 200)
	var result scanner.Node
	if err := json.Unmarshal([]byte(checked), &result); err != nil || result.ID != node.ID || result.Status != "ok" || result.Height == nil || *result.Height != 123 {
		t.Fatalf("unexpected single-node result: %s", checked)
	}
	call("POST", "/admin/nodes/test?network=solana", "", 200)
	var inventory []scanner.Node
	list := call("GET", "/admin/nodes?network=solana", "", 200)
	if err := json.Unmarshal([]byte(list), &inventory); err != nil || len(inventory) != 2 {
		t.Fatalf("unexpected inventory after batch: %s", list)
	}
	for _, row := range inventory {
		if row.Status != "ok" || row.Height == nil || *row.Height != 123 || row.CheckedAt == nil || row.Latency == nil {
			t.Fatalf("missing diagnostic data for node %d: %+v", row.ID, row)
		}
	}
	for _, suffix := range []string{"", "/strict"} {
		b := call("GET", "/transactions/solana/"+signature+suffix, "", 200)
		if !strings.Contains(b, signature) {
			t.Fatal("missing transaction result", b)
		}
	}
	call("GET", "/transactions/solana/invalid", "", 400)
	call("GET", "/transactions/solana/invalid/strict", "", 400)
	call("GET", "/heights/solana", "", 200)
}
