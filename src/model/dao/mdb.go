package dao

import (
	"github.com/GMWalletApp/epusdt/config"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

var Mdb *gorm.DB

func DBInit() error {
	if err := config.ValidateDatabase(viper.GetViper()); err != nil {
		return err
	}
	if err := SqliteInit(); err != nil {
		return err
	}

	return MdbTableInit()
}
