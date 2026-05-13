package data

import (
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/constant"
	tonaddress "github.com/xssnick/tonutils-go/address"
)

// AddWalletAddress 创建钱包 (默认 tron 网络，用于 Telegram 添加)
func AddWalletAddress(address string) (*mdb.WalletAddress, error) {
	return AddWalletAddressWithNetwork(mdb.NetworkTron, address)
}

// isEVMNetwork 判断是否是 EVM 网络
func isEVMNetwork(network string) bool {
	switch network {
	case mdb.NetworkEthereum, mdb.NetworkBsc, mdb.NetworkPolygon, mdb.NetworkPlasma:
		return true
	}
	return false
}

func normalizeWalletNetwork(network string) string {
	return strings.ToLower(strings.TrimSpace(network))
}

// normalizeTonAddress collapses TON's three surface forms — bounceable
// (EQ…), non-bounceable (UQ…), and raw (0:hex…) — into a single canonical
// bounceable user-friendly string. Same underlying wallet, one storage
// key. Returns the input unchanged if it cannot be parsed so the caller
// surfaces validation errors at the DB layer.
func normalizeTonAddress(addr string) string {
	parsed, err := tonaddress.ParseAddr(addr)
	if err != nil {
		parsed, err = tonaddress.ParseRawAddr(addr)
		if err != nil {
			return addr
		}
	}
	return parsed.Bounce(true).String()
}

func normalizeWalletAddressByNetwork(network, address string) string {
	address = strings.TrimSpace(address)
	net := normalizeWalletNetwork(network)
	if isEVMNetwork(net) {
		return strings.ToLower(address)
	}
	if net == mdb.NetworkTon {
		return normalizeTonAddress(address)
	}
	return address
}

// AddWalletAddressWithNetwork 创建指定网络的钱包地址
func AddWalletAddressWithNetwork(network, address string) (*mdb.WalletAddress, error) {
	network = normalizeWalletNetwork(network)
	address = normalizeWalletAddressByNetwork(network, address)

	exist, err := GetWalletAddressByNetworkAndAddress(network, address)
	if err != nil {
		return nil, err
	}
	if exist.ID > 0 {
		return nil, constant.WalletAddressAlreadyExists
	}

	// Check for a soft-deleted record with the same (network, address) and restore it.
	deleted := new(mdb.WalletAddress)
	err = dao.Mdb.Unscoped().
		Where("network = ? AND address = ? AND deleted_at IS NOT NULL", network, address).
		Limit(1).Find(deleted).Error
	if err != nil {
		return nil, err
	}
	if deleted.ID > 0 {
		err = dao.Mdb.Unscoped().Model(deleted).Updates(map[string]interface{}{
			"deleted_at": nil,
			"status":     mdb.TokenStatusEnable,
		}).Error
		return deleted, err
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
	network = normalizeWalletNetwork(network)
	address = normalizeWalletAddressByNetwork(network, address)
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
	network = normalizeWalletNetwork(network)
	var list []mdb.WalletAddress
	err := dao.Mdb.Model(list).
		Where("status = ?", mdb.TokenStatusEnable).
		Where("network = ?", network).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	if isEVMNetwork(network) {
		for i := range list {
			list[i].Address = strings.ToLower(strings.TrimSpace(list[i].Address))
		}
	}
	if network == mdb.NetworkTon {
		for i := range list {
			list[i].Address = normalizeTonAddress(list[i].Address)
		}
	}
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
	network = normalizeWalletNetwork(network)
	var list []mdb.WalletAddress
	err := dao.Mdb.Model(list).Where("network = ?", network).Find(&list).Error
	if err != nil {
		return nil, err
	}
	if isEVMNetwork(network) {
		for i := range list {
			list[i].Address = strings.ToLower(strings.TrimSpace(list[i].Address))
		}
	}
	if network == mdb.NetworkTon {
		for i := range list {
			list[i].Address = normalizeTonAddress(list[i].Address)
		}
	}
	return list, err
}

// ChangeWalletAddressStatus 启用禁用钱包
func ChangeWalletAddressStatus(id uint64, status int) error {
	err := dao.Mdb.Model(&mdb.WalletAddress{}).Where("id = ?", id).Update("status", status).Error
	return err
}
