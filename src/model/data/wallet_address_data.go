package data

import (
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/util/constant"
	"github.com/assimon/luuu/util/walletaddr"
)

// AddWalletAddress 创建钱包 (默认 tron 网络，用于 Telegram 添加)
func AddWalletAddress(address string) (*mdb.WalletAddress, error) {
	return AddWalletAddressWithNetwork(mdb.NetworkTron, address)
}

// AddWalletAddressWithNetwork 创建指定网络的钱包地址
func AddWalletAddressWithNetwork(network, address string) (*mdb.WalletAddress, error) {
	network = walletaddr.NormalizeNetwork(network)
	address = walletaddr.Normalize(network, address)
	if !walletaddr.Validate(network, address) {
		return nil, constant.InvalidWalletAddress
	}

	exist, err := GetWalletAddressByNetworkAndAddress(network, address)
	if err != nil {
		return nil, err
	}
	if exist.ID > 0 {
		return nil, constant.WalletAddressAlreadyExists
	}
	walletAddress := &mdb.WalletAddress{
		Network: network,
		Address: address,
		Status:  mdb.TokenStatusEnable,
	}
	err = dao.Mdb.Create(walletAddress).Error
	return walletAddress, err
}

// GetWalletAddressByNetworkAndAddress 通过网络和地址查询
func GetWalletAddressByNetworkAndAddress(network, address string) (*mdb.WalletAddress, error) {
	network = walletaddr.NormalizeNetwork(network)
	address = walletaddr.Normalize(network, address)
	walletAddress := new(mdb.WalletAddress)
	err := dao.Mdb.Model(walletAddress).
		Where("network = ?", network).
		Where("address = ?", address).
		Limit(1).Find(walletAddress).Error
	return walletAddress, err
}

// GetWalletAddressByToken 通过钱包地址获取address (兼容旧接口)
func GetWalletAddressByToken(address string) (*mdb.WalletAddress, error) {
	walletAddress := new(mdb.WalletAddress)
	err := dao.Mdb.Model(walletAddress).Limit(1).Find(walletAddress, "address = ?", address).Error
	return walletAddress, err
}

// GetWalletAddressById 通过id获取钱包
func GetWalletAddressById(id uint64) (*mdb.WalletAddress, error) {
	walletAddress := new(mdb.WalletAddress)
	err := dao.Mdb.Model(walletAddress).Limit(1).Find(walletAddress, id).Error
	return walletAddress, err
}

// DeleteWalletAddressById 通过id删除钱包
func DeleteWalletAddressById(id uint64) error {
	err := dao.Mdb.Where("id = ?", id).Delete(&mdb.WalletAddress{}).Error
	return err
}

// GetAvailableWalletAddress 获得所有可用的钱包地址
func GetAvailableWalletAddress() ([]mdb.WalletAddress, error) {
	var WalletAddressList []mdb.WalletAddress
	err := dao.Mdb.Model(WalletAddressList).Where("status = ?", mdb.TokenStatusEnable).Find(&WalletAddressList).Error
	return WalletAddressList, err
}

// GetAvailableWalletAddressByNetwork 获得指定网络的所有可用钱包地址
func GetAvailableWalletAddressByNetwork(network string) ([]mdb.WalletAddress, error) {
	network = walletaddr.NormalizeNetwork(network)
	var list []mdb.WalletAddress
	err := dao.Mdb.Model(list).
		Where("status = ?", mdb.TokenStatusEnable).
		Where("network = ?", network).
		Find(&list).Error
	return list, err
}

// GetAllWalletAddress 获得所有钱包地址
func GetAllWalletAddress() ([]mdb.WalletAddress, error) {
	var WalletAddressList []mdb.WalletAddress
	err := dao.Mdb.Model(WalletAddressList).Find(&WalletAddressList).Error
	return WalletAddressList, err
}

// GetAllWalletAddressByNetwork 获得指定网络的所有钱包地址
func GetAllWalletAddressByNetwork(network string) ([]mdb.WalletAddress, error) {
	network = walletaddr.NormalizeNetwork(network)
	var list []mdb.WalletAddress
	err := dao.Mdb.Model(list).Where("network = ?", network).Find(&list).Error
	return list, err
}

// ChangeWalletAddressStatus 启用禁用钱包
func ChangeWalletAddressStatus(id uint64, status int) error {
	err := dao.Mdb.Model(&mdb.WalletAddress{}).Where("id = ?", id).Update("status", status).Error
	return err
}
