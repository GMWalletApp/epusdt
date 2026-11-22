package route

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GMWalletApp/epusdt/model/data"
)

func TestCheckoutScriptSettingAndOrigin(t *testing.T) {
	t.Setenv("CASHIER_SCRIPT_ORIGIN", "https://pay.example.test")
	e, token := setupAdminTestEnv(t)
	configFor := func(host string) map[string]interface{} {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/payments/gmpay/v1/config", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return assertOK(t, rec)["data"].(map[string]interface{})["site"].(map[string]interface{})
	}
	save := func(group string, value any, wantOK bool) {
		t.Helper()
		item := map[string]any{"group": group, "key": "brand.cashier_script", "type": "string", "value": value}
		rec := doPutAdmin(e, "/admin/api/v1/settings", map[string]any{"items": []any{item}}, token)
		if row := assertOK(t, rec)["data"].([]interface{})[0].(map[string]interface{}); row["ok"] != wantOK {
			t.Fatalf("unexpected setting result: %v", row)
		}
	}
	if _, ok := configFor("shop.example.test")["cashier_script"]; ok {
		t.Fatal("unconfigured checkout advertises a script")
	}
	const snippet = "<script>window.chatSettings = {};</script>\n<script async src=\"https://chat.example.test/widget.js\"></script>"
	save("brand", snippet, true)
	save("rate", "wrong group", false)
	save("brand", true, false)
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	if shared := configFor("shop.example.test"); shared["cashier_script"] != snippet || shared["cashier_script_origin"] != nil {
		t.Fatal("shared host should only receive the isolated snippet")
	}
	if dedicated := configFor("pay.example.test:443"); dedicated["cashier_script"] != snippet || dedicated["cashier_script_origin"] != "https://pay.example.test" {
		t.Fatal("dedicated host missing the native mode origin")
	}
	for _, tc := range []struct {
		host, origin string
		want         int
	}{
		{"pay.example.test", "", 404}, {"pay.example.test:8443", "", 404},
		{"admin.example.test", "https://pay.example.test", 403}, {"admin.example.test", "null", 403},
	} {
		req := httptest.NewRequest(http.MethodGet, "https://"+tc.host+"/admin/api/v1/config", nil)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("admin on %s from %q: %d", tc.host, tc.origin, rec.Code)
		}
	}
	save("brand", "", true)
	if _, ok := configFor("pay.example.test")["cashier_script"]; ok {
		t.Fatal("reset left a widget enabled")
	}
}
