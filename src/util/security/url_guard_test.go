package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func withLookupIPAddr(t *testing.T, fn func(context.Context, string) ([]net.IPAddr, error)) {
	t.Helper()
	old := lookupIPAddr
	lookupIPAddr = fn
	t.Cleanup(func() {
		lookupIPAddr = old
	})
}

func TestValidatePublicHTTPURLAllowsPublicHTTPURL(t *testing.T) {
	withLookupIPAddr(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host != "example.com" {
			t.Fatalf("lookup host = %q, want example.com", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	})

	if err := ValidatePublicHTTPURL("https://example.com/api/"); err != nil {
		t.Fatalf("ValidatePublicHTTPURL returned error: %v", err)
	}
}

func TestValidatePublicHTTPURLRejectsUnsafeURLs(t *testing.T) {
	tests := []string{
		"",
		"://bad",
		"ftp://example.com/file",
		"https:///missing-host",
		"http://localhost/notify",
		"http://localhost./notify",
		"http://127.0.0.1/notify",
		"http://[::1]/notify",
		"http://10.0.0.1/notify",
		"http://172.16.0.1/notify",
		"http://172.31.255.255/notify",
		"http://192.168.1.1/notify",
		"http://169.254.1.1/notify",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.100.100.200/latest/meta-data/",
		"http://168.63.129.16/",
		"http://198.18.0.1/",
		"http://[64:ff9b::7f00:1]/",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if err := ValidatePublicHTTPURL(raw); err == nil {
				t.Fatalf("ValidatePublicHTTPURL(%q) returned nil, want error", raw)
			}
		})
	}
}

func TestDialPublicContextRejectsDNSRebinding(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	address := "merchant.example:" + port
	ip := net.ParseIP("93.184.216.34")
	withLookupIPAddr(t, func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: ip}}, nil
	})
	if err := ValidatePublicHTTPURL("http://" + address + "/callback"); err != nil {
		t.Fatal(err)
	}
	ip = net.ParseIP("127.0.0.1")
	transport := &http.Transport{DialContext: DialPublicContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	resp, err := client.Post("http://"+address+"/callback", "application/json", nil)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || requests.Load() != 0 {
		t.Fatalf("rebound callback: err=%v internal requests=%d", err, requests.Load())
	}
}

func TestDialPublicContextPinsValidatedAddress(t *testing.T) {
	withLookupIPAddr(t, func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	})
	previous := dialContext
	t.Cleanup(func() { dialContext = previous })
	dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "93.184.216.34:443" {
			t.Fatalf("dialed %q instead of validated IP", address)
		}
		conn, peer := net.Pipe()
		peer.Close()
		return conn, nil
	}
	conn, err := DialPublicContext(t.Context(), "tcp", "merchant.example:443")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestValidatePublicHTTPURLRejectsDomainResolvingToPrivateIP(t *testing.T) {
	withLookupIPAddr(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.1")}}, nil
	})

	if err := ValidatePublicHTTPURL("https://example.com/api/"); err == nil {
		t.Fatal("ValidatePublicHTTPURL returned nil, want error")
	}
}

func TestValidatePublicHTTPURLRejectsUnresolvableDomain(t *testing.T) {
	withLookupIPAddr(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return nil, errors.New("lookup failed")
	})

	if err := ValidatePublicHTTPURL("https://example.com/api/"); err == nil {
		t.Fatal("ValidatePublicHTTPURL returned nil, want error")
	}
}
