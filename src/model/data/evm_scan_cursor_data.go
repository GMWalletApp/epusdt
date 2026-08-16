package data

import (
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm/clause"
)

// GetEvmScanCursor fetches the persisted backfill cursor for a network.
func GetEvmScanCursor(network string) (*mdb.EvmScanCursor, error) {
	row := new(mdb.EvmScanCursor)
	err := dao.RuntimeDB.Model(row).
		Where("network = ?", strings.ToLower(strings.TrimSpace(network))).
		Limit(1).Find(row).Error
	return row, err
}

// UpsertEvmScanCursor stores the last confirmed block processed for a network.
func UpsertEvmScanCursor(network string, lastBlock int64) error {
	row := mdb.EvmScanCursor{
		Network:   strings.ToLower(strings.TrimSpace(network)),
		LastBlock: lastBlock,
	}
	return dao.RuntimeDB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "network"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_block", "updated_at"}),
	}).Create(&row).Error
}
