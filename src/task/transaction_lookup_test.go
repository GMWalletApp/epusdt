package task

import (
	"errors"
	"io"
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
		_, err := newTransactionClient(scanner.Client{URL: srv.URL}).automatic(t.Context(), "base", "hash")
		srv.Close()
		if err == nil || errors.Is(err, service.ErrTransactionNotFound) != tc.missing {
			t.Fatal(tc, err)
		}
	}
}

func TestTransactionRecheckNeedsOnlyTheRequestedTransaction(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		found          bool
	}{
		{"found", `{"network":"base","hash":"hash","result":{"hash":"hash"}}`, true},
		{"other network", `{"network":"ethereum","hash":"hash","result":{}}`, false},
		{"other hash", `{"network":"base","hash":"other","result":{}}`, false},
		{"missing result", `{"network":"base","hash":"hash","result":null}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-ChainScanner-Scan-Source", "excluded")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer srv.Close()
			_, err := newTransactionClient(scanner.Client{URL: srv.URL}).automatic(t.Context(), "base", "hash")
			if (err == nil) != tc.found {
				t.Errorf("found = %v, err = %v", tc.found, err)
			}
		})
	}
}
