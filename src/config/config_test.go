package config

import (
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/go-resty/resty/v2"
	"github.com/spf13/viper"
)

func installSettingsGetter(t *testing.T, values map[string]string) {
	t.Helper()

	oldGetter := SettingsGetString
	SettingsGetString = func(key string) string {
		return values[key]
	}
	t.Cleanup(func() {
		SettingsGetString = oldGetter
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func installMockHTTPClient(t *testing.T, handler roundTripFunc) {
	t.Helper()

	oldFactory := http_client.ClientFactory
	http_client.ClientFactory = func() *resty.Client {
		client := resty.NewWithClient(&http.Client{Transport: handler})
		client.SetTimeout(10 * time.Second)
		return client
	}
	t.Cleanup(func() {
		http_client.ClientFactory = oldFactory
	})
}

func TestNormalizeConfiguredPathUsesExplicitFile(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	configPath := filepath.Join(root, "custom.env")
	if err := os.WriteFile(configPath, []byte("app_name=test\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, err := normalizeConfiguredPath(configPath)
	if err != nil {
		t.Fatalf("normalize explicit file: %v", err)
	}
	if got != configPath {
		t.Fatalf("config path = %s, want %s", got, configPath)
	}
}

func TestNormalizeConfiguredPathUsesExplicitDirectory(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	configPath := filepath.Join(root, ".env")
	if err := os.WriteFile(configPath, []byte("app_name=test\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, err := normalizeConfiguredPath(root)
	if err != nil {
		t.Fatalf("normalize explicit directory: %v", err)
	}
	if got != configPath {
		t.Fatalf("config path = %s, want %s", got, configPath)
	}
}

func TestResolveConfigFilePathUsesCurrentDirectoryByDefault(t *testing.T) {
	t.Helper()

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldCwd) }()

	root := t.TempDir()
	configPath := filepath.Join(root, ".env")
	if err := os.WriteFile(configPath, []byte("app_name=test\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Setenv("EPUSDT_CONFIG", "")
	SetConfigPath("")

	got, err := resolveConfigFilePath()
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}

	gotAbs, err := filepath.Abs(got)
	if err != nil {
		t.Fatalf("abs got: %v", err)
	}
	wantAbs, err := filepath.Abs(configPath)
	if err != nil {
		t.Fatalf("abs want: %v", err)
	}

	gotReal, err := filepath.EvalSymlinks(gotAbs)
	if err != nil {
		t.Fatalf("eval symlinks got: %v", err)
	}
	wantReal, err := filepath.EvalSymlinks(wantAbs)
	if err != nil {
		t.Fatalf("eval symlinks want: %v", err)
	}

	if gotReal != wantReal {
		t.Fatalf("config path = %s, want %s", gotReal, wantReal)
	}
}

func TestResolveConfigFilePathPrefersExplicitOverEnv(t *testing.T) {
	t.Helper()

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldCwd) }()

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	envDir := filepath.Join(root, "from-env")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env dir: %v", err)
	}
	envPath := filepath.Join(envDir, ".env")
	if err := os.WriteFile(envPath, []byte("app_name=env\n"), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}

	flagDir := filepath.Join(root, "from-flag")
	if err := os.MkdirAll(flagDir, 0o755); err != nil {
		t.Fatalf("mkdir flag dir: %v", err)
	}
	flagPath := filepath.Join(flagDir, ".env")
	if err := os.WriteFile(flagPath, []byte("app_name=flag\n"), 0o644); err != nil {
		t.Fatalf("write flag config: %v", err)
	}

	t.Setenv("EPUSDT_CONFIG", envDir)
	SetConfigPath(flagDir)
	defer SetConfigPath("")

	got, err := resolveConfigFilePath()
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	if got != flagPath {
		t.Fatalf("config path = %s, want %s", got, flagPath)
	}
}

func TestCopyMissingStaticFilesCopiesAssetsWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "app-static")
	dst := filepath.Join(root, "data-static")

	if err := os.MkdirAll(filepath.Join(src, "images"), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.html"), []byte("source-index"), 0o644); err != nil {
		t.Fatalf("write source index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "payment.js"), []byte("source-payment"), 0o644); err != nil {
		t.Fatalf("write source payment: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "images", "logo.png"), []byte("source-logo"), 0o644); err != nil {
		t.Fatalf("write source logo: %v", err)
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "index.html"), []byte("custom-index"), 0o644); err != nil {
		t.Fatalf("write existing destination index: %v", err)
	}

	if err := copyMissingStaticFiles(src, dst); err != nil {
		t.Fatalf("copy missing static files: %v", err)
	}

	index, err := os.ReadFile(filepath.Join(dst, "index.html"))
	if err != nil {
		t.Fatalf("read destination index: %v", err)
	}
	if string(index) != "custom-index" {
		t.Fatalf("existing index was overwritten: %q", string(index))
	}

	payment, err := os.ReadFile(filepath.Join(dst, "payment.js"))
	if err != nil {
		t.Fatalf("read copied payment: %v", err)
	}
	if string(payment) != "source-payment" {
		t.Fatalf("payment.js = %q, want source-payment", string(payment))
	}

	logo, err := os.ReadFile(filepath.Join(dst, "images", "logo.png"))
	if err != nil {
		t.Fatalf("read copied nested asset: %v", err)
	}
	if string(logo) != "source-logo" {
		t.Fatalf("logo.png = %q, want source-logo", string(logo))
	}
}

func TestGetUsdtRatePrefersForcedRateList(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("API_RATE_URL", "")

	apiCalled := false
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		apiCalled = true
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     http.StatusText(http.StatusInternalServerError),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	installSettingsGetter(t, map[string]string{
		"rate.forced_rate_list": `{"cny":{"usdt":0.13793103448275862},"usd":{"trx":123.45}}`,
		"rate.api_url":          "https://rate.example.test",
	})

	got := GetUsdtRate()
	if math.Abs(got-7.25) > 1e-9 {
		t.Fatalf("GetUsdtRate() = %v, want 7.25", got)
	}
	if rate := GetRateForCoin("trx", "usd"); rate != 123.45 {
		t.Fatalf("GetRateForCoin(trx, usd) = %v, want 123.45", rate)
	}
	if apiCalled {
		t.Fatalf("rate API should not be called when rate.forced_rate_list has positive rate")
	}
}

func TestGetUsdtRateUsesAPIWhenAdminOverrideIsNotPositive(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("API_RATE_URL", "")

	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/cny.json" {
			t.Fatalf("rate api path = %s, want /cny.json", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.14635}}`)),
			Request:    r,
		}, nil
	})

	installSettingsGetter(t, map[string]string{
		"rate.forced_rate_list": `{"cny":{"usdt":0}}`,
		"rate.api_url":          "https://rate.example.test",
	})

	got := GetUsdtRate()
	want := 1 / 0.14635
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("GetUsdtRate() = %v, want %v", got, want)
	}

	rate := GetRateForCoin("usdt", "cny")
	if math.Abs(rate-0.14635) > 1e-9 {
		t.Fatalf("GetRateForCoin(usdt, cny) = %v, want 0.14635", rate)
	}
}

func TestGetRateForCoinUsesAPIWhenForcedPairMissing(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("API_RATE_URL", "")

	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/cny.json" {
			t.Fatalf("rate api path = %s, want /cny.json", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"cny":{"usdt":0.14635}}`)),
			Request:    r,
		}, nil
	})

	installSettingsGetter(t, map[string]string{
		"rate.forced_rate_list": `{"usd":{"trx":123.45}}`,
		"rate.api_url":          "https://rate.example.test",
	})

	rate := GetRateForCoin("usdt", "cny")
	if math.Abs(rate-0.14635) > 1e-9 {
		t.Fatalf("GetRateForCoin(usdt, cny) = %v, want 0.14635", rate)
	}
}

func TestGetUsdtRateReturnsZeroWhenAPIUnavailableWithoutAdminOverride(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("API_RATE_URL", "")

	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	installSettingsGetter(t, map[string]string{
		"rate.forced_rate_list": `{"cny":{"usdt":0}}`,
		"rate.api_url":          "https://rate.example.test",
	})

	if got := GetUsdtRate(); got != 0 {
		t.Fatalf("GetUsdtRate() = %v, want 0", got)
	}
	if got := GetRateForCoin("usdt", "cny"); got != 0 {
		t.Fatalf("GetRateForCoin(usdt, cny) = %v, want 0", got)
	}
}

func TestGetRateForCoinCallsRateAPIOnceForUsdtCnyFailure(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("API_RATE_URL", "")

	callCount := 0
	installMockHTTPClient(t, func(r *http.Request) (*http.Response, error) {
		callCount++
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	installSettingsGetter(t, map[string]string{
		"rate.forced_rate_list": `{"cny":{"usdt":0}}`,
		"rate.api_url":          "https://rate.example.test",
	})

	if got := GetRateForCoin("usdt", "cny"); got != 0 {
		t.Fatalf("GetRateForCoin(usdt, cny) = %v, want 0", got)
	}
	if callCount != 1 {
		t.Fatalf("rate api call count = %d, want 1", callCount)
	}
}
