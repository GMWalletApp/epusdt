package testutil

import (
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
		&mdb.NotificationChannel{},
		&mdb.Chain{},
		&mdb.ChainToken{},
		&mdb.RpcNode{},
		&mdb.AdminUser{},
	)
	mustMigrate(t, runtimeDB, &mdb.TransactionLock{})

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

	// Seed all standard chains as enabled so IsChainEnabled checks pass.
	for _, network := range []string{
		mdb.NetworkTron, mdb.NetworkSolana, mdb.NetworkEthereum,
		mdb.NetworkBsc, mdb.NetworkPolygon, mdb.NetworkPlasma,
		mdb.NetworkTon,
	} {
		mainDB.Create(&mdb.Chain{Network: network, Enabled: true})
	}

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
		Key:   "rate.forced_usdt_rate",
		Value: "1.0",
		Type:  "string",
	}).Error; err != nil {
		t.Fatalf("seed rate.forced_usdt_rate: %v", err)
	}

	return func() {
		closeDB(t, runtimeDB)
		closeDB(t, mainDB)
		dao.Mdb = nil
		dao.RuntimeDB = nil
		config.SettingsGetString = nil
		viper.Reset()
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
