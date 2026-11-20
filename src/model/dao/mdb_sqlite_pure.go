//go:build !sqlite_cgo

package dao

import (
	"github.com/libtnb/sqlite"
	"gorm.io/gorm"
)

func openDB(dsn string, cfg *gorm.Config) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(dsn+"?_journal_mode=WAL&_busy_timeout=5000"), cfg)
	if err != nil {
		return nil, err
	}
	return db, nil
}
