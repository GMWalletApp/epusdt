package scanneradmin

import (
	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScannerConnectionCapabilities(t *testing.T) {
	conn := config.ScannerConnection{AdminToken: strings.Repeat("a", 32), ConsumerToken: strings.Repeat("b", 32)}
	regDB, err := scanner.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer regDB.Close()
	reg, err := scanner.NewNodeRegistry(t.Context(), regDB, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := reg.Handler(map[string]string{"pay": conn.ConsumerToken}, conn.AdminToken)
	if err != nil {
		t.Fatal(err)
	}
	modern := httptest.NewServer(handler)
	defer modern.Close()
	conn.URL = modern.URL
	for _, embedded := range []bool{false, true} {
		conn.Embedded = embedded
		info, err := Connection(t.Context(), conn)
		if err != nil || !info.NodeSettings || !info.Migration || len(info.Protocols["ton"]) == 0 || (info.Target != "") == embedded {
			t.Fatal("capabilities", err)
		}
	}
	conn.Embedded = false
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+conn.AdminToken {
			t.Error("unexpected capability request")
		}
		switch r.URL.Path {
		case "/v1/admin/capabilities":
			http.NotFound(w, r)
		case "/v1/admin/nodes":
			w.Write([]byte("[]"))
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer legacy.Close()
	conn.URL = legacy.URL
	info, err := Connection(t.Context(), conn)
	if err != nil || info.NodeSettings || info.Migration || info.Target != legacy.URL || len(info.Protocols["base"]) == 0 {
		t.Fatal("legacy compatibility", err)
	}
	conn.URL = modern.URL
	conn.AdminToken = strings.Repeat("x", 32)
	if _, err = Connection(t.Context(), conn); err == nil {
		t.Fatal("unauthorized upstream treated as legacy")
	}
}
