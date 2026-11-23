package route

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/labstack/echo/v4"
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
	unauthedStream := httptest.NewRecorder()
	e.ServeHTTP(unauthedStream, httptest.NewRequest("GET", base+"/admin/progress/stream", nil))
	if unauthedStream.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated progress stream: %d", unauthedStream.Code)
	}
	var progress []struct {
		Network, Status string
		Success         *uint64 `json:"scan_success_count"`
		Failure         *uint64 `json:"scan_failure_count"`
	}
	if err := json.Unmarshal([]byte(call("GET", "/admin/progress", "", 200)), &progress); err != nil || len(progress) != 1 || progress[0].Network != "solana" || progress[0].Status != "idle" {
		t.Fatalf("scanner progress: %+v, %v", progress, err)
	}
	if p := progress[0]; p.Success == nil || p.Failure == nil || *p.Success != 0 || *p.Failure != 0 {
		t.Fatalf("idle scanner counters: %+v", p)
	}
	const retryPath = "/admin/scan-retry-rate"
	unauthorized := httptest.NewRecorder()
	e.ServeHTTP(unauthorized, httptest.NewRequest("GET", base+retryPath, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated retry rate: %d", unauthorized.Code)
	}
	for _, path := range []string{retryPath, retryPath + "?network=solana"} {
		var rows []map[string]any
		body := call("GET", path, "", http.StatusOK)
		if err := json.Unmarshal([]byte(body), &rows); err != nil || len(rows) != 1 {
			t.Fatalf("retry rate snapshot: %s", body)
		}
		row := rows[0]
		if rate, ok := row["retry_rate"]; !ok || rate != nil || row["network"] != "solana" || row["total_attempts"] != 0.0 || row["retry_attempts"] != 0.0 {
			t.Fatalf("unexpected initial retry rate: %s", body)
		}
	}
	call("GET", retryPath+"?network=unknown", "", http.StatusBadRequest)
	call("GET", retryPath+"?network=solana&network=solana", "", http.StatusBadRequest)
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
	call("PUT", "/admin/blacklist/keyword%3Aprovider", "", 204)
	call("PUT", "/admin/blacklist/exact%3Aother.example", "", 204)
	call("DELETE", "/admin/blacklist/exact%3Aother.example", "", 204)
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
			value = map[string]any{"slot": 123, "meta": map[string]any{"err": nil}, "transaction": map[string]any{"signatures": []string{signature}, "message": map[string]any{"recentBlockhash": "block", "instructions": []any{}, "accountKeys": []string{"account"}}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": value})
	})
	rpc := httptest.NewServer(handleRPC)
	defer rpc.Close()
	second := httptest.NewServer(handleRPC)
	defer second.Close()
	call("POST", "/admin/nodes/fallback", fmt.Sprintf(`{"network":"solana","protocol":"http","url":%q}`, second.URL), 201)
	var node scanner.Node
	legacy := mdb.RpcNode{Network: "solana", Type: "http", Url: rpc.URL, Enabled: true, Purpose: "general", Weight: 2}
	if err := dao.Mdb.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	var migration data.NodeMigration
	if err := json.Unmarshal([]byte(call("GET", "/admin/migration", "", 200)), &migration); err != nil || migration.Pending != 1 || !migration.External {
		t.Fatal("migration preview", err, migration)
	}
	payload, _ := json.Marshal(migration.MigrationTarget)
	call("POST", "/admin/migration", string(payload), 200)
	imported, err := reg.List(t.Context(), "solana")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range imported {
		if n.URL == rpc.URL {
			node = n
		}
	}
	if node.ID == 0 {
		t.Fatal("missing migrated voter")
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
	if b := call("GET", "/transactions/solana/"+signature, "", 200); !strings.Contains(b, signature) {
		t.Fatal(b)
	}
	// Disable the fallback to isolate the regular node's manual-only policy.
	for _, row := range inventory {
		if row.ID != node.ID {
			call("PATCH", fmt.Sprintf("/admin/nodes/%d", row.ID), `{"enabled":false}`, 200)
		}
	}
	call("PATCH", fmt.Sprintf("/admin/nodes/%d", node.ID), `{"enabled":false}`, 200)
	call("GET", "/transactions/solana/"+signature, "", 503)
	call("PATCH", fmt.Sprintf("/admin/nodes/%d", node.ID), `{"enabled":true,"purpose":"manual_verify"}`, 200)
	call("GET", "/transactions/solana/"+signature, "", 503)
	call("GET", "/transactions/solana/"+signature+"?manual=true", "", 200)
	call("PATCH", fmt.Sprintf("/admin/nodes/%d", node.ID), `{"purpose":"general"}`, 200)
	call("GET", "/transactions/solana/invalid", "", 400)
	call("GET", "/transactions/solana/invalid/strict", "", 400)
	call("GET", "/heights/solana", "", 200)
}

func TestScannerProgressStreamRecoversAndCancelsUpstream(t *testing.T) {
	const adminToken = "scanner-stream-private-admin-token"
	var requests atomic.Int32
	started, stopped := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/progress" || r.Header.Get("Authorization") != "Bearer "+adminToken {
			t.Errorf("unexpected scanner request to %s", r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch requests.Add(1) {
		case 1:
			// A legacy scanner supplies only its historical fields.
			fmt.Fprintln(w, `[
				{"network":"base","cursor_height":7,"status":"ok"}
			]`)
		case 2:
			http.Error(w, "upstream diagnostic: "+adminToken, http.StatusServiceUnavailable)
		case 3:
			fmt.Fprintln(w, `[{"network":"base","cursor_height":9,"status":"ok","scan_success_count":3,"scan_failure_count":1}]`)
		default:
			close(started)
			<-r.Context().Done()
			close(stopped)
		}
	}))
	defer upstream.Close()
	e := echo.New()
	e.GET("/stream", scannerProgressStream(config.ScannerConnection{URL: upstream.URL, AdminToken: adminToken}, 5*time.Millisecond))
	api := httptest.NewServer(e)
	defer api.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api.URL+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream response: %s, %v", resp.Status, resp.Header)
	}
	input := bufio.NewScanner(resp.Body)
	for _, want := range []string{
		"retry: 5000", "",
		"event: progress", `data: [{"network":"base","cursor_height":7,"status":"ok"}]`, "",
		"event: scanner_error", "data: {}", "",
		"event: progress", `data: [{"network":"base","cursor_height":9,"status":"ok","scan_success_count":3,"scan_failure_count":1}]`, "",
	} {
		if !input.Scan() || input.Text() != want {
			t.Fatalf("stream line = %q, want %q, err %v", input.Text(), want, input.Err())
		}
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("next scanner request did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("closing the dashboard left an upstream request active")
	}
}
