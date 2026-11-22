package install

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
)

func resetInstallTestState(t *testing.T) {
	t.Helper()

	closeInstallDatabases()
	dao.ResetMdbTableInitForTest()
	config.SetConfigPath("")
	viper.Reset()

	t.Cleanup(func() {
		closeInstallDatabases()
		dao.ResetMdbTableInitForTest()
		config.SetConfigPath("")
		viper.Reset()
	})
}

func newInstallTestAPI(envPath string) (*echo.Echo, *installHandler) {
	h := &installHandler{envFilePath: envPath, done: make(chan struct{})}
	e := echo.New()
	e.POST("/install", h.Submit)
	return e, h
}

func submitInstallRequest(t *testing.T, e *echo.Echo, payload string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/install", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func assertDoneClosed(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler did not close done channel within timeout")
	}
}

func changeInstalledAdminPassword(t *testing.T, envPath, newPassword string) {
	t.Helper()

	if err := initInstallConfig(envPath); err != nil {
		t.Fatalf("reopen install config: %v", err)
	}
	if err := initInstallDatabases(); err != nil {
		t.Fatalf("reopen install databases: %v", err)
	}
	defer closeInstallDatabases()

	user, err := data.GetAdminUserByUsername("admin")
	if err != nil {
		t.Fatalf("load admin user: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("expected seeded admin user")
	}
	if err := data.UpdateAdminUserPassword(uint64(user.ID), newPassword); err != nil {
		t.Fatalf("change admin password: %v", err)
	}
}

func TestInstallDefaults(t *testing.T) {
	d := InstallDefaults()
	if d.AppName != "epusdt" {
		t.Errorf("AppName = %q, want epusdt", d.AppName)
	}
	if d.HttpBindAddr != "127.0.0.1" {
		t.Errorf("HttpBindAddr = %q, want 127.0.0.1", d.HttpBindAddr)
	}
	if d.HttpBindPort != 8000 {
		t.Errorf("HttpBindPort = %d, want 8000", d.HttpBindPort)
	}
	if d.OrderExpirationTime != 10 {
		t.Errorf("OrderExpirationTime = %d, want 10", d.OrderExpirationTime)
	}
}

func TestWriteEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	req := &InstallRequest{
		AppName:             "myapp",
		AppURI:              "http://1.2.3.4:8000",
		HttpBindAddr:        "0.0.0.0",
		HttpBindPort:        9000,
		RuntimeRootPath:     "./runtime",
		LogSavePath:         "./logs",
		OrderExpirationTime: 15,
		OrderNoticeMaxRetry: 3,
	}
	if err := writeEnvFile(path, req, false); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"app_name=myapp",
		"app_uri=http://1.2.3.4:8000",
		"http_listen=0.0.0.0:9000",
		"order_expiration_time=15",
		"order_notice_max_retry=3",
		"db_type=sqlite",
		"install=false",
		"chainscanner_transport=ws",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("env file missing %q\ncontent:\n%s", want, content)
		}
	}
}

func TestInstallAPIDefaults(t *testing.T) {
	h := &installHandler{done: make(chan struct{})}
	e := echo.New()
	e.GET("/install/defaults", h.GetDefaults)

	req := httptest.NewRequest(http.MethodGet, "/install/defaults", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["app_name"] != "epusdt" {
		t.Errorf("app_name = %v, want epusdt", body["app_name"])
	}
	if body["http_bind_addr"] != "127.0.0.1" {
		t.Errorf("http_bind_addr = %v, want 127.0.0.1", body["http_bind_addr"])
	}
	if body["http_bind_port"] != float64(8000) {
		t.Errorf("http_bind_port = %v, want 8000", body["http_bind_port"])
	}
}

func TestInstallServerRootRedirectsToInstall(t *testing.T) {
	dir := t.TempDir()
	e, _ := newInstallServer(filepath.Join(dir, ".env"), http.FS(fstest.MapFS{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/install" {
		t.Fatalf("Location = %q, want /install", got)
	}
}

func TestInstallServerServesSPAOnInstallRoute(t *testing.T) {
	dir := t.TempDir()
	const wantBody = "install-ui"
	frontend := fstest.MapFS{"index.html": {Data: []byte(wantBody)}}
	e, _ := newInstallServer(filepath.Join(dir, ".env"), http.FS(frontend))

	req := httptest.NewRequest(http.MethodGet, "/install", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != wantBody {
		t.Fatalf("body = %q, want %q", body, wantBody)
	}
}

func TestInstallAPISubmitReturnsInitialPassword(t *testing.T) {
	resetInstallTestState(t)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	e, h := newInstallTestAPI(envPath)

	payload := `{"app_name":"testapp","app_uri":"http://10.0.0.1:8000","http_bind_addr":"0.0.0.0","http_bind_port":8000,"order_expiration_time":10,"order_notice_max_retry":1}`
	rec := submitInstallRequest(t, e, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if got, _ := body["message"].(string); got == "" {
		t.Fatalf("expected success message, got body=%v", body)
	}
	initPassword, _ := body["init_password"].(string)
	if strings.TrimSpace(initPassword) == "" {
		t.Fatalf("expected non-empty init_password, got body=%v", body)
	}
	assertDoneClosed(t, h.done)

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("env file not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "app_uri=http://10.0.0.1:8000") {
		t.Errorf("env file missing app_uri; content:\n%s", content)
	}
	if !strings.Contains(content, "http_listen=0.0.0.0:8000") {
		t.Errorf("env file missing http_listen; content:\n%s", content)
	}
	if !strings.Contains(content, "install=false") {
		t.Errorf("env file missing install=false; content:\n%s", content)
	}
}

func TestInstallAPISubmitRepeatInstallReturnsSamePasswordWhilePlaintextExists(t *testing.T) {
	resetInstallTestState(t)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	payload := `{"app_name":"testapp","app_uri":"http://10.0.0.1:8000","http_bind_addr":"0.0.0.0","http_bind_port":8000,"order_expiration_time":10,"order_notice_max_retry":1}`

	e1, h1 := newInstallTestAPI(envPath)
	rec1 := submitInstallRequest(t, e1, payload)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first install status = %d, want 200; body: %s", rec1.Code, rec1.Body.String())
	}
	body1 := decodeBody(t, rec1)
	password1, _ := body1["init_password"].(string)
	if strings.TrimSpace(password1) == "" {
		t.Fatalf("expected first init_password, got body=%v", body1)
	}
	assertDoneClosed(t, h1.done)

	e2, h2 := newInstallTestAPI(envPath)
	rec2 := submitInstallRequest(t, e2, payload)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second install status = %d, want 200; body: %s", rec2.Code, rec2.Body.String())
	}
	body2 := decodeBody(t, rec2)
	password2, _ := body2["init_password"].(string)
	if password2 != password1 {
		t.Fatalf("expected repeat install to return same init_password %q, got %q", password1, password2)
	}
	assertDoneClosed(t, h2.done)
}

func TestInstallAPISubmitRepeatInstallOmitsPasswordAfterPasswordChange(t *testing.T) {
	resetInstallTestState(t)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	payload := `{"app_name":"testapp","app_uri":"http://10.0.0.1:8000","http_bind_addr":"0.0.0.0","http_bind_port":8000,"order_expiration_time":10,"order_notice_max_retry":1}`

	e1, h1 := newInstallTestAPI(envPath)
	rec1 := submitInstallRequest(t, e1, payload)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first install status = %d, want 200; body: %s", rec1.Code, rec1.Body.String())
	}
	assertDoneClosed(t, h1.done)

	changeInstalledAdminPassword(t, envPath, "new-password-456")

	e2, h2 := newInstallTestAPI(envPath)
	rec2 := submitInstallRequest(t, e2, payload)
	if rec2.Code != http.StatusOK {
		t.Fatalf("repeat install status = %d, want 200; body: %s", rec2.Code, rec2.Body.String())
	}
	body2 := decodeBody(t, rec2)
	if got, ok := body2["init_password"]; ok {
		t.Fatalf("expected init_password to be omitted after password change, got %v", got)
	}
	if got, _ := body2["message"].(string); got == "" {
		t.Fatalf("expected success message, got body=%v", body2)
	}
	assertDoneClosed(t, h2.done)
}

func TestInstallAPISubmitInitFailureKeepsInstallMode(t *testing.T) {
	resetInstallTestState(t)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	e, h := newInstallTestAPI(envPath)
	payload := `{"app_name":"testapp","app_uri":"http://10.0.0.1:8000","http_bind_addr":"0.0.0.0","http_bind_port":8000,"runtime_root_path":"/dev/null/runtime","order_expiration_time":10,"order_notice_max_retry":1}`
	rec := submitInstallRequest(t, e, payload)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", rec.Code, rec.Body.String())
	}
	select {
	case <-h.done:
		t.Fatal("handler should not close done channel on init failure")
	case <-time.After(100 * time.Millisecond):
	}
	contentBytes, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env after init failure: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "install=true") {
		t.Fatalf("expected env to keep install=true after failure; content:\n%s", content)
	}
}

func TestInstallAPISubmitMissingURI(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	e, _ := newInstallTestAPI(envPath)
	rec := submitInstallRequest(t, e, `{"app_name":"x"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if _, err := os.Stat(envPath); err == nil {
		t.Error("env file should not have been written for invalid request")
	}
}

func TestInstallAPISubmitInvalidPort(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	e, _ := newInstallTestAPI(envPath)
	rec := submitInstallRequest(t, e, `{"app_uri":"http://example.com","http_bind_port":99999}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(envPath); err == nil {
		t.Error("env file should not have been written for invalid port")
	}
}

func TestInstallerPreservesUnsupportedDatabaseConfig(t *testing.T) {
	for _, legacy := range []string{"db_type=postgres", "db_type=mysql", "db_type=unknown", "mysql_database=payments", "postgres_host=localhost"} {
		t.Run(legacy, func(t *testing.T) {
			resetInstallTestState(t)
			path := filepath.Join(t.TempDir(), ".env")
			original := legacy + "\ninstall=true\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := writeEnvFile(path, &InstallRequest{}, true); err == nil || !strings.Contains(err.Error(), "SQLite") {
				t.Fatalf("installer database check: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != original {
				t.Fatalf("existing config changed: %q (error %v)", got, err)
			}
			if err := initInstallConfig(path); err != nil {
				t.Fatal(err)
			}
			if err := dao.DBInit(); err == nil || !strings.Contains(err.Error(), "SQLite") {
				t.Fatalf("startup database check: %v", err)
			}
			if _, err := os.Stat(config.GetPrimarySqlitePath()); !os.IsNotExist(err) {
				t.Fatalf("unexpected SQLite database after failed startup: %v", err)
			}
		})
	}
}
