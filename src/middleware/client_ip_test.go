package middleware

import (
	"net"
	"net/http/httptest"
	"testing"
)

func TestClientIPExtractorTrustsOnlyConfiguredProxies(t *testing.T) {
	direct, err := NewClientIPExtractor("")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.50:43120"
	req.Header.Set("X-Forwarded-For", "198.51.100.8")
	if got := direct(req); got != "203.0.113.50" {
		t.Fatalf("direct peer IP = %q", got)
	}

	trusted, err := NewClientIPExtractor("10.0.0.0/8, 192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = "10.1.2.3:43120"
	req.Header.Set("X-Forwarded-For", "198.51.100.8, 10.2.3.4")
	if got := trusted(req); got != "198.51.100.8" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}

	req.RemoteAddr = "203.0.113.50:43120"
	req.Header.Set("X-Forwarded-For", "198.51.100.8")
	if got := trusted(req); got != "203.0.113.50" {
		t.Fatalf("untrusted peer IP = %q", got)
	}
}

func TestClientIPExtractorRequiresExplicitTrust(t *testing.T) {
	for _, peer := range []string{
		"10.20.30.40", "172.16.0.1", "192.168.1.1", "127.0.0.1", "169.254.1.1",
		"fd00::1", "::1", "fe80::1",
	} {
		t.Run(peer, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = net.JoinHostPort(peer, "43120")
			req.Header.Set("X-Forwarded-For", "198.51.100.8")
			for _, configured := range []string{"", "192.0.2.10", peer} {
				extract, err := NewClientIPExtractor(configured)
				if err != nil {
					t.Fatal(err)
				}
				want := peer
				if configured == peer {
					want = "198.51.100.8"
				}
				if got := extract(req); got != want {
					t.Errorf("configured=%q: client IP = %q, want %q", configured, got, want)
				}
				if configured == "192.0.2.10" {
					req.RemoteAddr = "192.0.2.10:43120"
					req.Header.Set("X-Forwarded-For", "198.51.100.8, "+peer)
					if got := extract(req); got != peer {
						t.Errorf("untrusted intermediate proxy: client IP = %q, want %q", got, peer)
					}
					req.RemoteAddr = net.JoinHostPort(peer, "43120")
					req.Header.Set("X-Forwarded-For", "198.51.100.8")
				}
			}
		})
	}
}

func TestClientIPExtractorRejectsInvalidProxy(t *testing.T) {
	if _, err := NewClientIPExtractor("not-an-ip"); err == nil {
		t.Fatal("invalid proxy address accepted")
	}
}
