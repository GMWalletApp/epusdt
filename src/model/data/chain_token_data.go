package data

import (
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// ListEnabledChainTokens returns enabled tokens, optionally filtered by network.
// An empty network returns all enabled tokens across all networks.
func ListEnabledChainTokens(network string) ([]mdb.ChainToken, error) {
	var rows []mdb.ChainToken
	tx := dao.Mdb.Model(&mdb.ChainToken{}).Where("enabled = ?", true)
	if n := strings.ToLower(strings.TrimSpace(network)); n != "" {
		tx = tx.Where("network = ?", n)
	}
	err := tx.Order("network ASC, id ASC").Find(&rows).Error
	return rows, err
}

// ListChainTokens returns rows optionally filtered by network.
func ListChainTokens(network string) ([]mdb.ChainToken, error) {
	var rows []mdb.ChainToken
	tx := dao.Mdb.Model(&mdb.ChainToken{})
	if network != "" {
		tx = tx.Where("network = ?", strings.ToLower(network))
	}
	err := tx.Order("id ASC").Find(&rows).Error
	return rows, err
}

// ListEnabledChainTokensByNetwork returns the enabled tokens for a network.
// Scanners call this to learn which contract addresses to subscribe to.
func ListEnabledChainTokensByNetwork(network string) ([]mdb.ChainToken, error) {
	var rows []mdb.ChainToken
	err := dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ?", strings.ToLower(network)).
		Where("enabled = ?", true).
		Find(&rows).Error
	return rows, err
}

// GetEnabledChainTokenByContract finds one enabled token by (network,
// contract_address). Used by EVM/TRON scanners to resolve the symbol
// and decimals of an incoming transfer. Returns (zero row, nil) if no
// match. The contract address match is case-insensitive.
func GetEnabledChainTokenByContract(network, contractAddress string) (*mdb.ChainToken, error) {
	row := new(mdb.ChainToken)
	addr := strings.TrimSpace(contractAddress)
	if addr == "" {
		return row, nil
	}
	err := dao.Mdb.Model(row).
		Where("network = ?", strings.ToLower(strings.TrimSpace(network))).
		Where("enabled = ?", true).
		Where("LOWER(contract_address) = ?", strings.ToLower(addr)).
		Limit(1).Find(row).Error
	return row, err
}

// GetEnabledChainTokenBySymbol finds one enabled token by (network, symbol).
// Used by Solana native-SOL path where there's no contract address.
func GetEnabledChainTokenBySymbol(network, symbol string) (*mdb.ChainToken, error) {
	row := new(mdb.ChainToken)
	sym := strings.TrimSpace(symbol)
	if sym == "" {
		return row, nil
	}
	err := dao.Mdb.Model(row).
		Where("network = ?", strings.ToLower(strings.TrimSpace(network))).
		Where("enabled = ?", true).
		Where("UPPER(symbol) = ?", strings.ToUpper(sym)).
		Limit(1).Find(row).Error
	return row, err
}

// GetConfiguredChainToken returns metadata for a reserved payment even when
// the merchant has stopped offering this token for new payments.
func GetConfiguredChainToken(network, symbol string) (*mdb.ChainToken, error) {
	row := new(mdb.ChainToken)
	err := dao.Mdb.Where("network = ? AND UPPER(symbol) = ?", strings.ToLower(network), strings.ToUpper(symbol)).
		Limit(1).Find(row).Error
	return row, err
}

// GetChainTokenByID fetches by primary key.
func GetChainTokenByID(id uint64) (*mdb.ChainToken, error) {
	row := new(mdb.ChainToken)
	err := dao.Mdb.Model(row).Limit(1).Find(row, id).Error
	return row, err
}

// CreateChainToken inserts a row.
// If a soft-deleted row with the same (network, symbol) exists it is
// restored with the incoming field values rather than creating a duplicate
// that would violate the unique index.
func CreateChainToken(row *mdb.ChainToken) error {
	deleted := new(mdb.ChainToken)
	err := dao.Mdb.Unscoped().
		Where("network = ? AND symbol = ? AND deleted_at IS NOT NULL", row.Network, row.Symbol).
		Limit(1).Find(deleted).Error
	if err != nil {
		return err
	}
	if deleted.ID > 0 {
		err = dao.Mdb.Unscoped().Model(deleted).Updates(map[string]interface{}{
			"deleted_at":       nil,
			"contract_address": row.ContractAddress,
			"decimals":         row.Decimals,
			"enabled":          row.Enabled,
			"min_amount":       row.MinAmount,
		}).Error
		if err != nil {
			return err
		}
		row.ID = deleted.ID
		return nil
	}
	return dao.Mdb.Create(row).Error
}

// UpdateChainTokenFields patches mutable columns.
func UpdateChainTokenFields(id uint64, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return dao.Mdb.Model(&mdb.ChainToken{}).Where("id = ?", id).Updates(fields).Error
}

// DeleteChainTokenByID soft-deletes the row.
func DeleteChainTokenByID(id uint64) error {
	return dao.Mdb.Where("id = ?", id).Delete(&mdb.ChainToken{}).Error
}
