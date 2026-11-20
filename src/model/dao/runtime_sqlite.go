package dao

import (
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/gookit/color"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

var RuntimeDB *gorm.DB

func RuntimeInit() error {
	var err error
	runtimePath := config.GetRuntimeSqlitePath()
	color.Green.Printf("[runtime_db] sqlite filename: %s\n", runtimePath)
	RuntimeDB, err = openDB(runtimePath, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
		Logger: logger.Default.LogMode(logger.Error),
	})
	if err != nil {
		color.Red.Printf("[runtime_db] sqlite open DB,err=%s\n", err)
		return err
	}

	// The runtime database is write-heavy (transaction locks and scan cursors).
	// Serialize those writes through one connection; queue_concurrency controls
	// workers, not the number of concurrent SQLite writers.
	if _, err = configureSQLite(RuntimeDB, 1); err != nil {
		color.Red.Printf("[runtime_db] sqlite connDB err:%s", err.Error())
		return err
	}
	if err = RuntimeDB.Exec("DROP INDEX IF EXISTS transaction_lock_token_amount_uindex").Error; err != nil {
		return err
	}
	if err = RuntimeDB.Exec("DROP INDEX IF EXISTS transaction_lock_address_token_amount_uindex").Error; err != nil {
		return err
	}
	if err = RuntimeDB.Exec("DROP INDEX IF EXISTS transaction_lock_network_address_token_amount_uindex").Error; err != nil {
		return err
	}
	if err = RuntimeDB.AutoMigrate(&mdb.TransactionLock{}); err != nil {
		color.Red.Printf("[runtime_db] sqlite migrate DB(TransactionLock),err=%s\n", err)
		return err
	}
	if err = RuntimeDB.AutoMigrate(&mdb.EvmScanCursor{}); err != nil {
		color.Red.Printf("[runtime_db] sqlite migrate DB(EvmScanCursor),err=%s\n", err)
		return err
	}

	log.Sugar.Debug("[runtime_db] sqlite connDB success")
	return nil
}
