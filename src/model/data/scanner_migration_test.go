package data

import (
	"encoding/json"
	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/scanneradmin"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyScannerImportConsentConflictsAndRetry(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	ctx := t.Context()
	store, err := scanner.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry, err := scanner.NewNodeRegistry(ctx, store, []scanner.Network{{Name: "base"}})
	if err != nil {
		t.Fatal(err)
	}
	conn := config.ScannerConnection{AdminToken: strings.Repeat("a", 32), ConsumerToken: strings.Repeat("b", 32)}
	handler, err := registry.Handler(map[string]string{"epusdt": conn.ConsumerToken}, conn.AdminToken)
	if err != nil {
		t.Fatal(err)
	}
	posted := 0
	interrupt := true
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posted++
			if posted == 2 && interrupt {
				http.Error(w, "interrupted", 503)
				return
			}
		} else if r.URL.Path != "/v1/admin/migration-manifest" && r.URL.Path != "/v1/admin/routes" {
			t.Errorf("unexpected read %s", r.URL.Path)
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	conn.URL = remote.URL
	add := func(raw, key, purpose string, enabled bool) {
		t.Helper()
		n := mdb.RpcNode{Network: "base", Type: "http", Url: raw, ApiKey: key, Purpose: purpose, Enabled: true, Weight: 8}
		if err := dao.Mdb.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		if !enabled {
			if err := dao.Mdb.Model(&n).Update("enabled", false).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	add("https://one.example:443/tenant?key=p", "private-key", "manual_verify", false)
	add("https://ONE.example/tenant?key=p", "private-key", "manual_verify", false)
	add("https://one.example/tenant?key=q", "other-key", "general", true)
	add("https://one.example/other?key=p", "other-key", "general", true)
	add("https://conflict.example", "one-key", "general", true)
	add("https://conflict.example/", "different-key", "general", true)
	add("https://mainnet.base.org", "conflicting-built-in-key", "general", true)
	preview, err := MigrateScannerNodes(ctx, conn, false, nil)
	if err != nil || posted != 0 || !preview.External || preview.Pending != 4 || len(preview.Conflicts) != 3 {
		t.Fatalf("preview %+v, posts=%d err=%v", preview, posted, err)
	}
	raw, _ := json.Marshal(preview)
	if strings.Contains(string(raw), "private-key") || strings.Contains(string(raw), "tenant") {
		t.Fatal("private options leaked in preview")
	}
	wrong := preview.MigrationTarget
	wrong.StoreID = "wrong"
	for _, confirmation := range []*MigrationTarget{nil, &wrong} {
		if _, err := MigrateScannerNodes(ctx, conn, true, confirmation); err == nil || posted != 0 {
			t.Fatal("transmitted without confirmation")
		}
	}
	if _, err := MigrateScannerNodes(ctx, conn, true, &preview.MigrationTarget); err == nil {
		t.Fatal("missing interruption")
	}
	interrupt = false
	res, err := MigrateScannerNodes(ctx, conn, true, &preview.MigrationTarget)
	if err != nil || res.Completed != 4 || res.Pending != 0 || len(res.Conflicts) != 3 {
		t.Fatalf("retry %+v %v", res, err)
	}
	rows, err := registry.List(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, n := range rows {
		if strings.Contains(n.URL, "one.example") {
			count++
			if strings.Contains(n.URL, "/tenant?key=p") {
				if n.Enabled || n.Weight != 8 || n.Purpose != "manual_verify" {
					t.Fatal("lost migration settings")
				}
				if err := registry.Delete(ctx, n.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if count != 3 {
		t.Fatalf("expected three distinct full URLs, got %d", count)
	}
	calls := posted
	if _, err := MigrateScannerNodes(ctx, conn, true, &preview.MigrationTarget); err != nil || posted != calls {
		t.Fatal("replayed completed imports", err)
	}
	// Simulate interruption after remote commit but before the local receipt.
	var missing mdb.ScannerNodeMigration
	if err := dao.Mdb.Where("store_id=? AND legacy_id=?", res.StoreID, 3).First(&missing).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Delete(&missing).Error; err != nil {
		t.Fatal(err)
	}
	if res, err = MigrateScannerNodes(ctx, conn, true, &preview.MigrationTarget); err != nil || posted != calls || res.Completed != 4 {
		t.Fatal("could not reconcile existing matching node", err)
	}
	// Production payment selection must use the migrated scanner store.
	previous := scanneradmin.CandidateInventory
	scanneradmin.CandidateInventory = scanneradmin.Inventory
	defer func() { scanneradmin.CandidateInventory = previous }()
	old := runtimeScanner.Load()
	UseScannerNodes(conn)
	defer runtimeScanner.Store(old)
	candidates, err := ListManualPaymentRpcCandidates("base", "http")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, n := range candidates {
		if strings.Contains(n.Url, "one.example") {
			found++
			if !n.Enabled || n.ApiKey != "other-key" {
				t.Fatal("incorrect routing settings")
			}
		}
	}
	if found != 2 {
		t.Fatalf("usable migrated candidates %d", found)
	}
}
