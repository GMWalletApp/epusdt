package dao

import (
	"path/filepath"
	"testing"

	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

func TestSQLiteDefaultAndReopen(t *testing.T) {
	previousDB, previousLogger := Mdb, log.Sugar
	log.Sugar = zap.NewNop().Sugar()
	ResetMdbTableInitForTest()
	t.Cleanup(func() {
		Mdb, log.Sugar = previousDB, previousLogger
		ResetMdbTableInitForTest()
		viper.Reset()
	})
	viper.Set("sqlite_database_filename", filepath.Join(t.TempDir(), "existing.db"))
	open := func() func() error {
		t.Helper()
		if err := DBInit(); err != nil {
			t.Fatal(err)
		}
		db, err := Mdb.DB()
		if err != nil {
			t.Fatal(err)
		}
		return db.Close
	}
	closeDB := open()
	row := mdb.Setting{Key: "test.restart", Value: "persisted"}
	if err := Mdb.Create(&row).Error; err != nil {
		_ = closeDB()
		t.Fatal(err)
	}
	if err := closeDB(); err != nil {
		t.Fatal(err)
	}

	ResetMdbTableInitForTest()
	viper.Set("db_type", "SQLite")
	viper.Set("mysql_host", "unused-legacy-setting")
	closeDB = open()
	defer closeDB()
	var restored mdb.Setting
	if err := Mdb.First(&restored, row.ID).Error; err != nil || restored.Value != row.Value {
		t.Fatalf("reopened setting = %q, want %q (error %v)", restored.Value, row.Value, err)
	}
}
