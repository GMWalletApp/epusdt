package data

import (
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func GetProviderOrderByTradeIDAndProvider(tradeID string, provider string) (*mdb.ProviderOrder, error) {
	row := new(mdb.ProviderOrder)
	err := dao.Mdb.Model(row).
		Where("trade_id = ?", tradeID).
		Where("provider = ?", provider).
		Limit(1).
		Find(row).Error
	return row, err
}

func CreateProviderOrderWithTransaction(tx *gorm.DB, row *mdb.ProviderOrder) error {
	return tx.Model(row).Create(row).Error
}

func UpsertProviderOrderCreatingWithTransaction(tx *gorm.DB, row *mdb.ProviderOrder) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "trade_id"}, {Name: "provider"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"provider_order_id",
			"receive_address",
			"pay_url",
			"amount",
			"coin",
			"status",
			"notify_raw",
			"updated_at",
		}),
	}).Create(row).Error
}

func UpdateProviderOrderCreated(tradeID string, provider string, providerOrderID string, payURL string) error {
	return UpdateProviderOrderCreatedWithTransaction(dao.Mdb, tradeID, provider, providerOrderID, payURL)
}

func UpdateProviderOrderCreatedWithTransaction(tx *gorm.DB, tradeID string, provider string, providerOrderID string, payURL string) error {
	return tx.Model(&mdb.ProviderOrder{}).
		Where("trade_id = ?", tradeID).
		Where("provider = ?", provider).
		Updates(map[string]interface{}{
			"provider_order_id": providerOrderID,
			"pay_url":           payURL,
			"status":            mdb.ProviderOrderStatusPending,
		}).Error
}

func MarkProviderOrderFailed(tradeID string, provider string) error {
	return dao.Mdb.Model(&mdb.ProviderOrder{}).
		Where("trade_id = ?", tradeID).
		Where("provider = ?", provider).
		Update("status", mdb.ProviderOrderStatusFailed).Error
}

func SaveProviderOrderNotify(tradeID string, provider string, notifyRaw string) error {
	return dao.Mdb.Model(&mdb.ProviderOrder{}).
		Where("trade_id = ?", tradeID).
		Where("provider = ?", provider).
		Update("notify_raw", notifyRaw).Error
}

func MarkProviderOrderPaid(tradeID string, provider string, notifyRaw string) error {
	return dao.Mdb.Model(&mdb.ProviderOrder{}).
		Where("trade_id = ?", tradeID).
		Where("provider = ?", provider).
		Updates(map[string]interface{}{
			"status":     mdb.ProviderOrderStatusPaid,
			"notify_raw": notifyRaw,
		}).Error
}

func MarkProviderOrderExpired(tradeID string, provider string) error {
	return dao.Mdb.Model(&mdb.ProviderOrder{}).
		Where("trade_id = ?", tradeID).
		Where("provider = ?", provider).
		Where("status IN ?", []string{mdb.ProviderOrderStatusCreating, mdb.ProviderOrderStatusPending}).
		Update("status", mdb.ProviderOrderStatusExpired).Error
}

func GetSubOrderByTokenPayProvider(parentTradeID string, token string, payProvider string) (*mdb.Orders, error) {
	order := new(mdb.Orders)
	err := dao.Mdb.Model(order).
		Where("parent_trade_id = ?", parentTradeID).
		Where("token = ?", token).
		Where("pay_provider = ?", payProvider).
		Where("status = ?", mdb.StatusWaitPay).
		Limit(1).
		Find(order).Error
	return order, err
}

// ListPendingProviderOrdersByProvider returns provider_orders rows still
// waiting on the downstream provider, oldest first. Used by the X Layer
// poll job (the provider offers no webhook, so pending rows must be
// queried until they settle or terminate).
func ListPendingProviderOrdersByProvider(provider string) ([]mdb.ProviderOrder, error) {
	var rows []mdb.ProviderOrder
	err := dao.Mdb.Model(&mdb.ProviderOrder{}).
		Where("provider = ?", provider).
		Where("status = ?", mdb.ProviderOrderStatusPending).
		Order("updated_at ASC").
		Find(&rows).Error
	return rows, err
}
