package testutil

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	appLog "github.com/GMWalletApp/epusdt/util/log"
	"github.com/libtnb/sqlite"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func SetupTestDatabases(t testing.TB) func() {
	t.Helper()

	viper.Reset()
	viper.Set("app_uri", "https://example.com")
	viper.Set("order_expiration_time", 10)
	viper.Set("order_notice_max_retry", 2)
	viper.Set("callback_retry_base_seconds", 1)
	viper.Set("queue_concurrency", 4)
	viper.Set("queue_poll_interval_ms", 50)

	config.HTTPAccessLog = false
	config.SQLDebug = false
	config.LogLevel = "error"
	appLog.Sugar = zap.NewNop().Sugar()

	mainDB := mustOpenSQLite(t, filepath.Join(t.TempDir(), "main.db"))
	runtimeDB := mustOpenSQLite(t, filepath.Join(t.TempDir(), "runtime.db"))

	mustMigrate(t, mainDB,
		&mdb.Orders{},
		&mdb.ProviderOrder{},
		&mdb.WalletAddress{},
		&mdb.ApiKey{},
		&mdb.Setting{},
		&mdb.RateCache{},
		&mdb.NotificationChannel{},
		&mdb.Chain{},
		&mdb.ChainToken{},
		&mdb.RpcNode{},
		&mdb.AdminUser{},
	)
	mustMigrate(t, runtimeDB, &mdb.TransactionLock{}, &mdb.EvmScanCursor{})

	dao.Mdb = mainDB
	dao.RuntimeDB = runtimeDB
	config.SettingsGetString = func(key string) string {
		if dao.Mdb == nil {
			return ""
		}
		var row mdb.Setting
		if err := dao.Mdb.Where("`key` = ?", key).Take(&row).Error; err != nil {
			return ""
		}
		return row.Value
	}
	config.RateCacheLoad = func(base string) (config.RateCacheSnapshot, error) {
		var row mdb.RateCache
		err := dao.Mdb.Where("base = ?", base).Take(&row).Error
		if err != nil {
			return config.RateCacheSnapshot{}, err
		}
		return testRateCacheSnapshot(row), nil
	}
	config.RateCacheLoadAll = func() ([]config.RateCacheSnapshot, error) {
		var rows []mdb.RateCache
		err := dao.Mdb.Order("base ASC").Find(&rows).Error
		if err != nil {
			return nil, err
		}
		out := make([]config.RateCacheSnapshot, 0, len(rows))
		for _, row := range rows {
			out = append(out, testRateCacheSnapshot(row))
		}
		return out, nil
	}
	config.RateCacheSave = func(snapshot config.RateCacheSnapshot) error {
		rates, err := json.Marshal(snapshot.Rates)
		if err != nil {
			return err
		}
		row := mdb.RateCache{
			Base:          snapshot.Base,
			Rates:         string(rates),
			SourceURL:     snapshot.SourceURL,
			LastSuccessAt: snapshot.LastSuccessAt,
			LastAttemptAt: snapshot.LastAttemptAt,
			LastRefreshOK: snapshot.LastRefreshOK,
			LastError:     snapshot.LastError,
		}
		return dao.Mdb.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "base"}},
			DoUpdates: clause.AssignmentColumns([]string{"rates", "source_url", "last_success_at", "last_attempt_at", "last_refresh_ok", "last_error", "updated_at"}),
		}).Create(&row).Error
	}
	config.ResetRateCacheRuntime()

	// Seed all standard chains as enabled so IsChainEnabled checks pass.
	for _, network := range []string{
		mdb.NetworkTron, mdb.NetworkSolana, mdb.NetworkEthereum,
		mdb.NetworkBsc, mdb.NetworkPolygon, mdb.NetworkPlasma, mdb.NetworkTon,
	} {
		mainDB.Create(&mdb.Chain{Network: network, Enabled: true})
	}

	mainDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&[]mdb.ChainToken{
		{Network: mdb.NetworkTron, Symbol: "USDT", ContractAddress: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkEthereum, Symbol: "USDT", ContractAddress: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkBsc, Symbol: "USDT", ContractAddress: "0x55d398326f99059fF775485246999027B3197955", Decimals: 18, Enabled: true},
		{Network: mdb.NetworkTon, Symbol: "TON", ContractAddress: "", Decimals: 9, Enabled: true},
		{Network: mdb.NetworkTon, Symbol: "USDT", ContractAddress: "0:b113a994b5024a16719f69139328eb759596c38a25f59028b146fecdc3621dfe", Decimals: 6, Enabled: true},
	})

	// Seed two universal api_keys rows. Both usable for EPAY/GMPAY
	// flows; the numeric PID 1001 row lets legacy tests that submit
	// `pid=1001` still match.
	mainDB.Create(&mdb.ApiKey{
		Name: "test-default",
		Pid:  "test-token", SecretKey: "test-token",
		Status: mdb.ApiKeyStatusEnable,
	})
	mainDB.Create(&mdb.ApiKey{
		Name: "test-pid-1001",
		Pid:  "1001", SecretKey: "test-token",
		Status: mdb.ApiKeyStatusEnable,
	})
	if err := dao.Mdb.Create(&mdb.Setting{
		Group: "rate",
		Key:   "rate.forced_rate_list",
		Value: `{"cny":{"usdt":1}}`,
		Type:  "json",
	}).Error; err != nil {
		t.Fatalf("seed rate.forced_rate_list: %v", err)
	}

	return func() {
		closeDB(t, runtimeDB)
		closeDB(t, mainDB)
		dao.Mdb = nil
		dao.RuntimeDB = nil
		config.SettingsGetString = nil
		config.RateCacheLoad = nil
		config.RateCacheLoadAll = nil
		config.RateCacheSave = nil
		config.ResetRateCacheRuntime()
		viper.Reset()
	}
}

func testRateCacheSnapshot(row mdb.RateCache) config.RateCacheSnapshot {
	rates := make(map[string]float64)
	_ = json.Unmarshal([]byte(row.Rates), &rates)
	return config.RateCacheSnapshot{
		Base:          row.Base,
		Rates:         rates,
		SourceURL:     row.SourceURL,
		LastSuccessAt: row.LastSuccessAt,
		LastAttemptAt: row.LastAttemptAt,
		LastRefreshOK: row.LastRefreshOK,
		LastError:     row.LastError,
	}
}

func mustOpenSQLite(t testing.TB, path string) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle for %s: %v", path, err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	return db
}

func mustMigrate(t testing.TB, db *gorm.DB, models ...interface{}) {
	t.Helper()
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
}

func closeDB(t testing.TB, db *gorm.DB) {
	t.Helper()
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle close: %v", err)
	}
	if err = sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}
