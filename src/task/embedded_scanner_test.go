package task

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedScannerPersistsSettingsAcrossStarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scanner.sqlite")
	call := func(method, endpoint, token string, wantStatus int) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != wantStatus {
			t.Fatalf("status=%d err=%v", res.StatusCode, err)
		}
		return string(body)
	}
	var previousToken string
	for run := range 2 {
		func() {
			conn, stop, err := startEmbeddedScanner(context.Background(), path, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			u, err := url.Parse(conn.URL)
			if err != nil || u.Hostname() != "127.0.0.1" || u.Port() == "" {
				t.Fatal("expected local listener")
			}
			call("GET", conn.URL+"/v1/admin/progress", conn.ConsumerToken, 401)
			call("GET", conn.URL+"/v1/admin/progress", conn.AdminToken, 200)
			blacklist := conn.URL + "/v1/admin/blacklist"
			if run == 0 {
				previousToken = conn.AdminToken
				call("PUT", blacklist+"/example.com", conn.AdminToken, 204)
			} else {
				call("GET", blacklist, previousToken, 401)
				if !strings.Contains(call("GET", blacklist, conn.AdminToken, 200), "example.com") {
					t.Fatal("missing persisted blacklist entry")
				}
			}
		}()
	}
}
