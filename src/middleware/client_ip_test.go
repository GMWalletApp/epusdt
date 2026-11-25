package middleware

import (
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

func TestClientIPExtractorRejectsInvalidProxy(t *testing.T) {
	if _, err := NewClientIPExtractor("not-an-ip"); err == nil {
		t.Fatal("invalid proxy address accepted")
	}
}
