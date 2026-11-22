package version

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsNewerVersionSemVerRules(t *testing.T) {
	tests := []struct {
		name    string
		latest  string
		current string
		want    bool
		valid   bool
	}{
		{"same ignores build metadata", "v1.2.3+build.2", "1.2.3+build.1", false, true},
		{"patch update", "v1.2.4", "1.2.3", true, true},
		{"release beats rc", "1.2.3", "1.2.3-rc.1", true, true},
		{"rc does not beat release", "1.2.3-rc.2", "v1.2.3", false, true},
		{"rc ordering", "1.2.3-rc.10", "1.2.3-rc.2", true, true},
		{"numeric prerelease beats alpha", "1.2.3-1", "1.2.3-alpha", false, true},
		{"invalid current fails closed", "1.2.4", "1.2", false, false},
		{"leading zero fails", "1.2.3", "1.2.3-01", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsNewerVersion(tt.latest, tt.current)
			if (err == nil) != tt.valid {
				t.Fatalf("error=%v, valid=%v", err, tt.valid)
			}
			if got != tt.want {
				t.Fatalf("IsNewerVersion()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckerCachesSuccessfulReleaseAndJoinsRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.0.0", "name": "Stable", "html_url": "https://example.invalid/wrong-release",
			"body": "- fixes", "published_at": "2026-01-02T03:04:05Z",
		})
	}))
	defer server.Close()

	checker := NewCheckerWithClient(server.Client(), server.URL, time.Minute)
	results := make(chan *CheckResult, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			result, err := checker.CheckLatestRelease(context.Background(), "1.0.0", "acme/pay")
			results <- result
			errs <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if result := <-results; result == nil || !result.HasUpdate {
			t.Fatalf("unexpected result: %+v", result)
		} else if result.ReleaseURL != "https://github.com/acme/pay/releases/tag/v2.0.0" {
			t.Fatalf("release URL=%q, want stable release tag URL", result.ReleaseURL)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("GitHub calls=%d, want one coalesced request", got)
	}
	if _, err := checker.CheckLatestRelease(context.Background(), "2.0.0", "acme/pay"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cached GitHub calls=%d, want one", got)
	}
}

func TestCheckerMapsRateLimitAndNoRelease(t *testing.T) {
	for _, tt := range []struct {
		name   string
		code   int
		header string
		want   error
	}{
		{name: "rate limit", code: http.StatusForbidden, header: "0", want: ErrRateLimited},
		{name: "rate limit status", code: http.StatusTooManyRequests, want: ErrRateLimited},
		{name: "no release", code: http.StatusNotFound, want: ErrNoRelease},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.header != "" {
					w.Header().Set("X-RateLimit-Remaining", tt.header)
				}
				w.WriteHeader(tt.code)
			}))
			defer server.Close()
			checker := NewCheckerWithClient(server.Client(), server.URL, time.Minute)
			_, err := checker.CheckLatestRelease(context.Background(), "1.0.0", "acme/pay")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error=%v, want %v", err, tt.want)
			}
		})
	}
}
