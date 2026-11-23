package task

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/model/service"
)

func TestOnlyAnIndependentNotFoundResponseSuspendsConfirmation(t *testing.T) {
	for _, tc := range []struct {
		status           int
		source, evidence string
		missing          bool
	}{
		{404, "excluded", "not_found", true},
		{404, "", "", false},
		{404, "unobserved", "not_found", false},
		{503, "excluded", "not_found", false},
		{502, "excluded", "pending", false},
		{200, "", "", false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/transactions/base/hash" || r.URL.Query().Get("observed") != "true" {
				t.Error(r.URL)
			}
			w.Header().Set("X-ChainScanner-Scan-Source", tc.source)
			w.Header().Set("X-ChainScanner-Evidence", tc.evidence)
			w.WriteHeader(tc.status)
		}))
		_, err := newStrictClient(scanner.Client{URL: srv.URL}).automatic(t.Context(), "base", "hash")
		srv.Close()
		if err == nil || errors.Is(err, service.ErrTransactionNotFound) != tc.missing {
			t.Fatal(tc, err)
		}
	}
}
