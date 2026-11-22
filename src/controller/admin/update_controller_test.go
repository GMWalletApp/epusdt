package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/version"
	"github.com/labstack/echo/v4"
)

type fakeReleaseChecker struct {
	result  *version.CheckResult
	release *version.Release
	err     error
}

func (f fakeReleaseChecker) CheckLatestRelease(context.Context, string, string) (*version.CheckResult, error) {
	return f.result, f.err
}

func (f fakeReleaseChecker) FetchLatestRelease(context.Context, string) (*version.Release, error) {
	return f.release, f.err
}

func TestCheckSystemUpdateReturnsResult(t *testing.T) {
	original := systemReleaseChecker
	defer func() { systemReleaseChecker = original }()
	systemReleaseChecker = fakeReleaseChecker{result: &version.CheckResult{
		CurrentVersion: "v1.0.0", LatestVersion: "v1.1.0", HasUpdate: true,
		ReleaseURL: "https://github.com/GMWalletApp/epusdt/releases/tag/v1.1.0",
	}}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/v1/system/version/check", nil)
	rec := httptest.NewRecorder()
	if err := Ctrl.CheckSystemUpdate(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
}

func TestGetCurrentSystemVersionConfirmsReachedTargetWithoutReleaseLookup(t *testing.T) {
	original := config.BuildVersion
	config.BuildVersion = "v2.1.2"
	defer func() { config.BuildVersion = original }()

	for _, tt := range []struct {
		target    string
		confirmed bool
	}{
		{target: "v2.1.1", confirmed: true},
		{target: "v2.1.2", confirmed: true},
		{target: "v2.1.3", confirmed: false},
		{target: "invalid", confirmed: false},
	} {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/admin/api/v1/system/version/current?target_version="+tt.target, nil)
		rec := httptest.NewRecorder()
		if err := Ctrl.GetCurrentSystemVersion(e.NewContext(req, rec)); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("target=%q status=%d body=%s", tt.target, rec.Code, rec.Body.String())
		}
		var payload struct {
			Data CurrentVersionResult `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Data.CurrentVersion != "v2.1.2" || payload.Data.Confirmed != tt.confirmed {
			t.Fatalf("target=%q result=%+v", tt.target, payload.Data)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("Cache-Control=%q", rec.Header().Get("Cache-Control"))
		}
	}
}

func TestCheckSystemUpdateMapsFailureStates(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		code int
	}{
		{name: "rate limited", err: version.ErrRateLimited, code: http.StatusTooManyRequests},
		{name: "no release", err: version.ErrNoRelease, code: http.StatusNotFound},
		{name: "timeout", err: context.DeadlineExceeded, code: http.StatusGatewayTimeout},
		{name: "upstream failure", err: errors.New("connection reset"), code: http.StatusBadGateway},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := systemReleaseChecker
			defer func() { systemReleaseChecker = original }()
			systemReleaseChecker = fakeReleaseChecker{err: tt.err}
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/admin/api/v1/system/version/check", nil)
			rec := httptest.NewRecorder()
			if err := Ctrl.CheckSystemUpdate(e.NewContext(req, rec)); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tt.code {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tt.code, rec.Body.String())
			}
		})
	}
}
