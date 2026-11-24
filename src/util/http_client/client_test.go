package http_client

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCallbackClientBlocksPrivateTargetsAndProxyBypass(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("HTTP_PROXY", server.URL)
	for _, target := range []string{server.URL, "http://100.100.100.200/latest/meta-data/"} {
		client := GetCallbackHttpClient()
		if _, err := client.R().SetBody(`{"signature":"test-signature"}`).Post(target); err == nil {
			t.Fatalf("accepted private callback %s", target)
		}
		client.GetClient().CloseIdleConnections()
	}
	if requests.Load() != 0 {
		t.Fatalf("internal service received %d callbacks", requests.Load())
	}
}
