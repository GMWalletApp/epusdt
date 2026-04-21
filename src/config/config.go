package config

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/assimon/luuu/util/http_client"
	"github.com/spf13/viper"
	"github.com/tidwall/gjson"
)

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
	UsdtRate           float64
	RateApiUrl         string
	TRON_GRID_API_KEY  string
	BuildVersion       = "0.0.0-dev"
	BuildCommit        = "none"
	BuildDate          = "unknown"
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

	RateApiUrl = GetRateApiUrl()
	TRON_GRID_API_KEY = viper.GetString("tron_grid_api_key")
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

func GetApiAuthToken() string {
	return viper.GetString("api_auth_token")
}

func GetEpaySignKey() string {
	key := strings.TrimSpace(GetEpayKey())
	if key != "" {
		return key
	}
	return strings.TrimSpace(GetApiAuthToken())
}

func GetRateApiUrl() string {
	rateURL := viper.GetString("api_rate_url")
	if rateURL == "" {
		rateURL = os.Getenv("API_RATE_URL")
	}
	if rateURL == "" {
		log.Println("api_rate_url is empty")
	}
	RateApiUrl = rateURL
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
		}
	}

	baseURL := RateApiUrl
	if baseURL == "" {
		baseURL = GetRateApiUrl()
	}
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
	forcedUsdtRate := viper.GetFloat64("forced_usdt_rate")
	if forcedUsdtRate > 0 {
		return forcedUsdtRate
	}
	if UsdtRate <= 0 {
		return 6.4
	}
	return UsdtRate
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

func GetSolanaRpcUrl() string {
	rpcUrl := viper.GetString("solana_rpc_url")
	if rpcUrl == "" {
		return "https://api.mainnet-beta.solana.com"
	}
	return rpcUrl
}

func GetEthereumWsUrl() string {
	return strings.TrimSpace(viper.GetString("ethereum_ws_url"))
}

func GetEpayPid() int {
	return viper.GetInt("epay_pid")
}

func GetEpayKey() string {
	return viper.GetString("epay_key")
}

func GetEpayDefaultToken() string {
	token := strings.TrimSpace(viper.GetString("epay_default_token"))
	if token == "" {
		return "usdt"
	}
	return strings.ToLower(token)
}

func GetEpayDefaultCurrency() string {
	currency := strings.TrimSpace(viper.GetString("epay_default_currency"))
	if currency == "" {
		return "cny"
	}
	return strings.ToLower(currency)
}

func GetEpayDefaultNetwork() string {
	network := strings.TrimSpace(viper.GetString("epay_default_network"))
	if network == "" {
		return "tron"
	}
	return strings.ToLower(network)
}
