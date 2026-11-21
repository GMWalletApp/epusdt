package dao

import (
	stdlog "log"
	"os"
	"path/filepath"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/gookit/color"
	"github.com/spf13/viper"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// SqliteInit 数据库初始化
func SqliteInit() error {
	var err error
	dbFilename := config.GetPrimarySqlitePath()
	if err = os.MkdirAll(filepath.Dir(dbFilename), 0o755); err != nil {
		color.Red.Printf("[store_db] sqlite mkdir err=%s\n", err)
		return err
	}
	color.Green.Printf("[store_db] sqlite filename: %s\n", dbFilename)
	Mdb, err = openDB(dbFilename, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   viper.GetString("sqlite_table_prefix"),
			SingularTable: true,
		},
		Logger: logger.New(stdlog.New(os.Stdout, "", stdlog.LstdFlags), logger.Config{
			LogLevel:             logger.Error,
			SlowThreshold:        200 * time.Millisecond,
			ParameterizedQueries: true,
		}),
	})
	if err != nil {
		color.Red.Printf("[store_db] sqlite open DB,err=%s\n", err)
		// panic(err)
		return err
	}
	if config.SQLDebug {
		Mdb = Mdb.Debug()
	}
	if _, err = configureSQLite(Mdb, 1); err != nil {
		color.Red.Printf("[store_db] sqlite connDB err:%s", err.Error())
		return err
	}
	log.Sugar.Debug("[store_db] sqlite connDB success")
	return nil
}
