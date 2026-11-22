package config

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

var (
	HTTPAccessLog      bool
	SQLDebug           bool
	LogLevel           string
	RuntimePath        string
	LogSavePath        string
	StaticPath         string
	StaticFilePath     string
	UpdateRepository   string
	TgBotToken         string
	TgProxy            string
	TgManage           int64
	BuildVersion       = "0.0.0-dev"
	BuildCommit        = "none"
	BuildDate          = "unknown"
	BuildType          = "source"
	configRootPath     string
	explicitConfigPath string
)

func SetConfigPath(path string) {
	explicitConfigPath = strings.TrimSpace(path)
}

func Init() {
	configPath, err := resolveConfigFilePath()
	if err != nil {
		panic(err)
	}
	configRootPath = filepath.Dir(configPath)

	viper.SetConfigFile(configPath)
	err = viper.ReadInConfig()
	if err != nil {
		panic(err)
	}
	HTTPAccessLog = viper.GetBool("http_access_log")
	SQLDebug = viper.GetBool("sql_debug")
	LogLevel = normalizeLogLevel(viper.GetString("log_level"))
	StaticPath = normalizeStaticURLPath(viper.GetString("static_path"))
	StaticFilePath = filepath.Join(configRootPath, strings.TrimPrefix(StaticPath, "/"))
	UpdateRepository = normalizeUpdateRepository(viper.GetString("update_repository"))
	if err = ensureConfiguredStaticFiles(); err != nil {
		panic(err)
	}
	RuntimePath = resolvePathFromBase(configRootPath, viper.GetString("runtime_root_path"), filepath.Join(configRootPath, "runtime"))
	LogSavePath = resolvePathFromBase(RuntimePath, viper.GetString("log_save_path"), filepath.Join(RuntimePath, "logs"))
	mustMkdir(RuntimePath)
	mustMkdir(LogSavePath)
	TgBotToken = viper.GetString("tg_bot_token")
	TgProxy = viper.GetString("tg_proxy")
	TgManage = viper.GetInt64("tg_manage")
}

func mustMkdir(path string) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		panic(err)
	}
}

func ensureConfiguredStaticFiles() error {
	if strings.TrimSpace(StaticFilePath) == "" {
		return nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return err
	}

	srcDir := filepath.Join(filepath.Dir(exePath), "static")
	srcInfo, err := os.Stat(srcDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !srcInfo.IsDir() {
		return nil
	}

	srcAbs, err := filepath.Abs(srcDir)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(StaticFilePath)
	if err != nil {
		return err
	}
	if srcAbs == dstAbs {
		return nil
	}

	return copyMissingStaticFiles(srcAbs, dstAbs)
}

func copyMissingStaticFiles(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		if _, err = os.Stat(dstPath); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		return copyFile(path, dstPath)
	})
}

func copyFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}

	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "info", "warn", "error":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "info"
	}
}

func normalizeStaticURLPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return "/static"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

// normalizeUpdateRepository keeps the release source server-side and limits
// it to the owner/name form accepted by the GitHub API. The browser never
// supplies this value, which prevents turning the check endpoint into a URL
// proxy.
func normalizeUpdateRepository(repository string) string {
	repository = strings.TrimSpace(repository)
	if repository == "" {
		return "GMWalletApp/epusdt"
	}
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repository, "\\?#:") {
		return "GMWalletApp/epusdt"
	}
	return repository
}

// GetUpdateRepository returns the configured GitHub owner/name. Init must have
// run before serving requests; the fallback keeps tests and CLI commands safe.
func GetUpdateRepository() string {
	if UpdateRepository != "" {
		return UpdateRepository
	}
	return normalizeUpdateRepository(viper.GetString("update_repository"))
}

func resolvePathFromBase(basePath string, path string, fallback string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return fallback
	}
	if filepath.IsAbs(path) {
		return path
	}
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "\\")
	return filepath.Join(basePath, filepath.FromSlash(path))
}

func resolveConfigFilePath() (string, error) {
	if explicitConfigPath != "" {
		return normalizeConfiguredPath(explicitConfigPath)
	}
	if envPath := strings.TrimSpace(os.Getenv("EPUSDT_CONFIG")); envPath != "" {
		return normalizeConfiguredPath(envPath)
	}
	return normalizeConfiguredPath(".env")
}

// NeedsInstall returns true when the first-run install wizard should run.
// It returns true if the config file does not exist yet, or if the file
// exists and contains install=true (an explicit reset flag).
// Existing installs that have no install key are NOT affected.
func NeedsInstall() bool {
	envPath, exists := ResolveConfigPath()
	if !exists {
		return true
	}
	// Read just the install key without triggering full Init().
	v := viper.New()
	v.SetConfigFile(envPath)
	if err := v.ReadInConfig(); err != nil {
		// Unreadable config → treat as needing install.
		return true
	}
	return strings.EqualFold(strings.TrimSpace(v.GetString("install")), "true")
}

// ResolveConfigPath returns the absolute path to the .env file and whether
// it currently exists on disk. Unlike resolveConfigFilePath it does not
// return an error when the file is absent — callers (e.g. the install
// wizard) need the target path even before the file is written.
func ResolveConfigPath() (path string, exists bool) {
	candidate := explicitConfigPath
	if candidate == "" {
		if e := strings.TrimSpace(os.Getenv("EPUSDT_CONFIG")); e != "" {
			candidate = e
		} else {
			candidate = ".env"
		}
	}
	// Resolve relative paths against cwd.
	p := strings.TrimSpace(candidate)
	if !filepath.IsAbs(p) {
		cwd, err := os.Getwd()
		if err == nil {
			p = filepath.Join(cwd, p)
		}
	}
	info, err := os.Stat(p)
	if err == nil && info.IsDir() {
		p = filepath.Join(p, ".env")
		info, err = os.Stat(p)
	}
	if err != nil {
		return p, false
	}
	return p, true
}

func normalizeConfiguredPath(input string) (string, error) {
	path := strings.TrimSpace(input)
	if path == "" {
		path = ".env"
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(cwd, path)
	}

	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		path = filepath.Join(path, ".env")
		info, err = os.Stat(path)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("config file not found: %s", path)
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("config path must point to a file, got directory: %s", path)
	}
	return path, nil
}

func GetAppVersion() string {
	return BuildVersion
}

func GetBuildCommit() string {
	return BuildCommit
}

func GetBuildDate() string {
	return BuildDate
}

func GetAppName() string {
	appName := viper.GetString("app_name")
	if appName == "" {
		return "epusdt"
	}
	return appName
}

func GetAppUri() string {
	return viper.GetString("app_uri")
}

func GetRateApiUrl() string {
	// settings table wins (admin-configurable); .env and env var remain
	// as fallbacks for smooth migration from the old layout.
	if db := settingsRateApiUrl(); db != "" {
		return db
	}
	return GetRateApiUrlFromEnv()
}

// GetRateApiUrlFromEnv returns the rate API URL read only from the
// .env / environment variables, bypassing the settings table. Used
// by bootstrap to seed the settings table from the initial .env value.
func GetRateApiUrlFromEnv() string {
	rateURL := viper.GetString("api_rate_url")
	if rateURL == "" {
		rateURL = os.Getenv("API_RATE_URL")
	}
	if rateURL == "" {
		log.Println("api_rate_url is empty")
	}
	return rateURL
}

func GetOrderExpirationTime() int {
	timer := viper.GetInt("order_expiration_time")
	if timer <= 0 {
		return 10
	}
	return timer
}

func GetOrderExpirationTimeDuration() time.Duration {
	timer := GetOrderExpirationTime()
	return time.Minute * time.Duration(timer)
}

// ValidateDatabase rejects legacy database settings before opening a file or rewriting the config.
func ValidateDatabase(settings *viper.Viper) error {
	dbType := strings.TrimSpace(settings.GetString("db_type"))
	if strings.EqualFold(dbType, "sqlite") {
		return nil
	}
	if dbType != "" {
		return fmt.Errorf("unsupported db_type %q: EPUSDT supports only SQLite", dbType)
	}
	// Older releases defaulted to MySQL. Don't open an empty SQLite file over a legacy configuration.
	for _, backend := range []string{"mysql", "postgres"} {
		if settings.GetString(backend+"_database") != "" || settings.GetString(backend+"_host") != "" {
			return fmt.Errorf("legacy %s configuration detected: migrate your data to SQLite and set db_type=sqlite before starting", backend)
		}
	}
	return nil
}

func GetRuntimeSqlitePath() string {
	filename := viper.GetString("runtime_sqlite_filename")
	if filename == "" {
		filename = "epusdt-runtime.db"
	}
	if filepath.IsAbs(filename) {
		return filename
	}
	filename = strings.TrimPrefix(strings.TrimPrefix(filename, "/"), "\\")
	return filepath.Join(RuntimePath, filepath.FromSlash(filename))
}

func GetPrimarySqlitePath() string {
	filename := strings.TrimSpace(viper.GetString("sqlite_database_filename"))
	if filename == "" {
		return filepath.Join(configRootPath, "epusdt.db")
	}
	if filepath.IsAbs(filename) {
		return filename
	}
	filename = strings.TrimPrefix(strings.TrimPrefix(filename, "/"), "\\")
	return filepath.Join(configRootPath, filepath.FromSlash(filename))
}

func GetQueueConcurrency() int {
	concurrency := viper.GetInt("queue_concurrency")
	if concurrency <= 0 {
		return 10
	}
	return concurrency
}

func GetQueuePollInterval() time.Duration {
	interval := viper.GetInt("queue_poll_interval_ms")
	if interval <= 0 {
		interval = 1000
	}
	return time.Duration(interval) * time.Millisecond
}

func GetOrderNoticeMaxRetry() int {
	retry := viper.GetInt("order_notice_max_retry")
	if retry < 0 {
		return 0
	}
	return retry
}

func GetCallbackRetryBaseDuration() time.Duration {
	seconds := viper.GetInt("callback_retry_base_seconds")
	if seconds <= 0 {
		seconds = 5
	}
	return time.Duration(seconds) * time.Second
}
