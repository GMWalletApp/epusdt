package data

import (
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// ListChains returns all chain rows ordered by id.
func ListChains() ([]mdb.Chain, error) {
	var rows []mdb.Chain
	err := dao.Mdb.Model(&mdb.Chain{}).Order("id ASC").Find(&rows).Error
	return rows, err
}

// ListEnabledChains returns only rows with Enabled=true.
func ListEnabledChains() ([]mdb.Chain, error) {
	var rows []mdb.Chain
	err := dao.Mdb.Model(&mdb.Chain{}).
		Where("enabled = ?", true).
		Order("id ASC").Find(&rows).Error
	return rows, err
}

// GetChainByNetwork fetches one row by network key.
func GetChainByNetwork(network string) (*mdb.Chain, error) {
	row := new(mdb.Chain)
	err := dao.Mdb.Model(row).
		Where("network = ?", strings.ToLower(strings.TrimSpace(network))).
		Limit(1).Find(row).Error
	return row, err
}

// IsChainEnabled returns whether the row exists and Enabled=true. A
// missing row is treated as disabled — scanners must be able to
// short-circuit even before the admin creates the chain entry.
func IsChainEnabled(network string) bool {
	if strings.ToLower(strings.TrimSpace(network)) == mdb.NetworkArc {
		return true
	}
	row, err := GetChainByNetwork(network)
	if err != nil || row.ID == 0 {
		return false
	}
	return row.Enabled
}

// UpdateChainFields patches mutable columns.
func UpdateChainFields(network string, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return dao.Mdb.Model(&mdb.Chain{}).
		Where("network = ?", strings.ToLower(strings.TrimSpace(network))).
		Updates(fields).Error
}
