package data

import (
	"strings"

	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/util/constant"
)

func normalizeNetworkToken(network, token string) (string, string) {
	return strings.ToLower(strings.TrimSpace(network)), strings.ToUpper(strings.TrimSpace(token))
}

func AddSupportedAsset(network, token string, status int64) (*mdb.SupportedAsset, error) {
	network, token = normalizeNetworkToken(network, token)
	exist, err := GetSupportedAssetByNetworkAndToken(network, token)
	if err != nil {
		return nil, err
	}
	if exist.ID > 0 {
		return nil, constant.SupportedAssetAlreadyExists
	}

	deleted, err := getSupportedAssetByNetworkAndTokenUnscoped(network, token)
	if err != nil {
		return nil, err
	}
	if deleted.ID > 0 && deleted.DeletedAt.Valid {
		deleted.Status = status
		deleted.DeletedAt.Valid = false
		if err := dao.Mdb.Unscoped().Save(deleted).Error; err != nil {
			return nil, err
		}
		return deleted, nil
	}

	asset := &mdb.SupportedAsset{
		Network: network,
		Token:   token,
		Status:  status,
	}
	err = dao.Mdb.Create(asset).Error
	return asset, err
}

func getSupportedAssetByNetworkAndTokenUnscoped(network, token string) (*mdb.SupportedAsset, error) {
	network, token = normalizeNetworkToken(network, token)
	asset := new(mdb.SupportedAsset)
	err := dao.Mdb.Unscoped().Model(asset).
		Where("network = ?", network).
		Where("token = ?", token).
		Limit(1).Find(asset).Error
	return asset, err
}

func GetSupportedAssetByNetworkAndToken(network, token string) (*mdb.SupportedAsset, error) {
	network, token = normalizeNetworkToken(network, token)
	asset := new(mdb.SupportedAsset)
	err := dao.Mdb.Model(asset).
		Where("network = ?", network).
		Where("token = ?", token).
		Limit(1).Find(asset).Error
	return asset, err
}

func GetSupportedAssetByID(id uint64) (*mdb.SupportedAsset, error) {
	asset := new(mdb.SupportedAsset)
	err := dao.Mdb.Model(asset).Limit(1).Find(asset, id).Error
	return asset, err
}

func UpdateSupportedAsset(id uint64, network, token string, status int64) (*mdb.SupportedAsset, error) {
	network, token = normalizeNetworkToken(network, token)

	asset, err := GetSupportedAssetByID(id)
	if err != nil {
		return nil, err
	}
	if asset.ID <= 0 {
		return nil, constant.SupportedAssetNotFound
	}

	exist, err := GetSupportedAssetByNetworkAndToken(network, token)
	if err != nil {
		return nil, err
	}
	if exist.ID > 0 && exist.ID != id {
		return nil, constant.SupportedAssetAlreadyExists
	}

	asset.Network = network
	asset.Token = token
	asset.Status = status
	if err := dao.Mdb.Save(asset).Error; err != nil {
		return nil, err
	}
	return asset, nil
}

func DeleteSupportedAssetByID(id uint64) error {
	return dao.Mdb.Where("id = ?", id).Delete(&mdb.SupportedAsset{}).Error
}

func ListSupportedAssets(network string) ([]mdb.SupportedAsset, error) {
	network = strings.ToLower(strings.TrimSpace(network))
	var list []mdb.SupportedAsset
	tx := dao.Mdb.Model(&mdb.SupportedAsset{})
	if network != "" {
		tx = tx.Where("network = ?", network)
	}
	err := tx.Order("network asc, token asc").Find(&list).Error
	return list, err
}

func ListEnabledSupportedAssets() ([]mdb.SupportedAsset, error) {
	var list []mdb.SupportedAsset
	err := dao.Mdb.Model(&mdb.SupportedAsset{}).
		Where("status = ?", mdb.TokenStatusEnable).
		Order("network asc, token asc").
		Find(&list).Error
	return list, err
}

func IsSupportedAssetEnabled(network, token string) (bool, error) {
	asset, err := GetSupportedAssetByNetworkAndToken(network, token)
	if err != nil {
		return false, err
	}
	return asset.ID > 0 && asset.Status == mdb.TokenStatusEnable, nil
}
