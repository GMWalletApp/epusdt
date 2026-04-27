package config

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/spf13/viper"
	"github.com/tidwall/gjson"
)

// parseFloat is the minimal helper used by the settings bridge; kept
// here so settings_bridge.go stays import-free.
func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

var (
	HTTPAccessLog      bool
	SQLDebug           bool
	LogLevel           string
	MysqlDns           string
	RuntimePath        string
	LogSavePath        string
	StaticPath         string
	StaticFilePath     string
	TgBotToken         string
	TgProxy            string
	TgManage           int64
	BuildVersion       = "0.0.0-dev"
	BuildCommit        = "none"
	BuildDate          = "unknown"
	configRootPath     string
	explicitConfigPath string
)

func resolveExecutableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", err
	}
	return filepath.Dir(exePath), nil
}

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
	exeDir, err := resolveExecutableDir()
	if err != nil {
		panic(err)
	}
	StaticFilePath = filepath.Join(exeDir, "static")
	RuntimePath = resolvePathFromBase(configRootPath, viper.GetString("runtime_root_path"), filepath.Join(configRootPath, "runtime"))
	LogSavePath = resolvePathFromBase(RuntimePath, viper.GetString("log_save_path"), filepath.Join(RuntimePath, "logs"))
	mustMkdir(RuntimePath)
	mustMkdir(LogSavePath)
	MysqlDns = fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		url.QueryEscape(viper.GetString("mysql_user")),
		url.QueryEscape(viper.GetString("mysql_passwd")),
		fmt.Sprintf("%s:%s", viper.GetString("mysql_host"), viper.GetString("mysql_port")),
		viper.GetString("mysql_database"))
	TgBotToken = viper.GetString("tg_bot_token")
	TgProxy = viper.GetString("tg_proxy")
	TgManage = viper.GetInt64("tg_manage")
}

func mustMkdir(path string) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		panic(err)
	}
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

func GetRateForCoin(coin string, base string) float64 {
	coin = strings.ToLower(strings.TrimSpace(coin))
	base = strings.ToLower(strings.TrimSpace(base))
	if coin == "" || base == "" {
		return 0
	}
	if coin == base {
		return 1
	}
	if coin == "usdt" {
		switch base {
		case "usd":
			return 1
		case "cny":
			usdtRate := GetUsdtRate()
			if usdtRate > 0 {
				return 1 / usdtRate
			}
			return 0
		}
	}
	return getRateForCoinFromAPI(coin, base)
}

func getRateForCoinFromAPI(coin string, base string) float64 {
	baseURL := GetRateApiUrl()
	if baseURL == "" {
		log.Printf("rate api url is empty")
		return 0.0
	}
	if baseURL[len(baseURL)-1] != '/' {
		baseURL += "/"
	}

	client := http_client.GetHttpClient()
	resp, err := client.R().Get(baseURL + fmt.Sprintf("%s.json", base))
	if err != nil {
		log.Printf("call rate api error: %s", err.Error())
		return 0.0
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		log.Printf("call rate api unexpected status: %s", resp.Status())
		return 0.0
	}

	targetRate := 0.0
	gjson.GetBytes(resp.Body(), base).ForEach(func(key, value gjson.Result) bool {
		if key.String() == coin {
			targetRate = value.Float()
			return false
		}
		return true
	})
	return targetRate
}

func GetUsdtRate() float64 {
	// Only the admin setting can force the USDT/CNY rate. When the
	// setting is unset, zero, or negative, fall back to the rate API.
	if forced := settingsForcedUsdtRate(); forced > 0 {
		return forced
	}

	apiRate := getRateForCoinFromAPI("usdt", "cny")
	if apiRate > 0 {
		return 1 / apiRate
	}
	log.Printf("usdt/cny rate unavailable: rate.forced_usdt_rate <= 0 and rate api returned no data")
	return 0
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
