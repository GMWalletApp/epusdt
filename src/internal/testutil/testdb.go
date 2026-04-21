package testutil

import (
	"path/filepath"
	"testing"

	"github.com/assimon/luuu/config"
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/mdb"
	appLog "github.com/assimon/luuu/util/log"
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
	viper.Set("forced_usdt_rate", 1.0)
	viper.Set("app_uri", "https://example.com")
	viper.Set("order_expiration_time", 10)
	viper.Set("order_notice_max_retry", 2)
	viper.Set("callback_retry_base_seconds", 1)
	viper.Set("queue_concurrency", 4)
	viper.Set("queue_poll_interval_ms", 50)
	viper.Set("api_auth_token", "test-token")
	viper.Set("epay_key", "test-epay-key")
	viper.Set("epay_pid", 1)

	config.HTTPAccessLog = false
	config.SQLDebug = false
	config.LogLevel = "error"
	config.UsdtRate = 0
	appLog.Sugar = zap.NewNop().Sugar()

	mainDB := mustOpenSQLite(t, filepath.Join(t.TempDir(), "main.db"))
	runtimeDB := mustOpenSQLite(t, filepath.Join(t.TempDir(), "runtime.db"))

	mustMigrate(t, mainDB, &mdb.Orders{}, &mdb.WalletAddress{}, &mdb.SupportedAsset{})
	mustMigrate(t, runtimeDB, &mdb.TransactionLock{})

	dao.Mdb = mainDB
	dao.RuntimeDB = runtimeDB

	if err := mainDB.Create(&[]mdb.SupportedAsset{
		{Network: mdb.NetworkTron, Token: "USDT", Status: mdb.TokenStatusEnable},
		{Network: mdb.NetworkTron, Token: "TRX", Status: mdb.TokenStatusEnable},
		{Network: mdb.NetworkSolana, Token: "USDT", Status: mdb.TokenStatusEnable},
		{Network: mdb.NetworkEthereum, Token: "USDT", Status: mdb.TokenStatusEnable},
	}).Error; err != nil {
		t.Fatalf("seed supported assets: %v", err)
	}

	return func() {
		closeDB(t, runtimeDB)
		closeDB(t, mainDB)
		dao.Mdb = nil
		dao.RuntimeDB = nil
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
