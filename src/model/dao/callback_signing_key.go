package dao

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/constant"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PinCallbackSigningKey stores the PID and secret used to authenticate a merchant
// order. The order only stores the ID of this private version.
func PinCallbackSigningKey(tx *gorm.DB, key *mdb.ApiKey) (uint64, error) {
	if key == nil || key.ID == 0 || key.Pid == "" || key.SecretKey == "" {
		return 0, constant.OrderApiKeyUnavailableErr
	}
	encoded, _ := json.Marshal([3]string{strconv.FormatUint(key.ID, 10), key.Pid, key.SecretKey})
	digest := sha256.Sum256(encoded)
	row := mdb.CallbackSigningKey{
		Fingerprint: hex.EncodeToString(digest[:]),
		ApiKeyID:    key.ID,
		Pid:         key.Pid,
		SecretKey:   key.SecretKey,
	}
	result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "fingerprint"}}, DoNothing: true}).Create(&row)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		if err := tx.Select("id").Where("fingerprint = ?", row.Fingerprint).Take(&row).Error; err != nil {
			return 0, err
		}
	}
	return row.ID, nil
}

// Legacy orders have no record of earlier rotations. Freeze the credentials
// available at upgrade, including soft-deleted merchants, once per merchant.
func backfillCallbackSigningKeys(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		unpinned := tx.Unscoped().Model(&mdb.Orders{}).Where("callback_signing_key_id = 0 AND api_key_id > 0")
		var keys []mdb.ApiKey
		if err := tx.Unscoped().Where("id IN (?) AND pid <> '' AND secret_key <> ''", unpinned.Select("api_key_id")).Find(&keys).Error; err != nil {
			return err
		}
		for _, key := range keys {
			id, err := PinCallbackSigningKey(tx, &key)
			if err != nil {
				return err
			}
			if err := tx.Unscoped().Model(&mdb.Orders{}).Where("api_key_id = ? AND callback_signing_key_id = 0", key.ID).UpdateColumn("callback_signing_key_id", id).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
