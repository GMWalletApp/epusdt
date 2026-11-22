package data

import (
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm"
)

// merchantOrders is a read-only projection of one row per merchant order.
// Identity, fiat price, timestamps, status and callback belong to the parent.
// Before pay_by_sub_id existed, settlement copied the child transaction hash
// onto the parent. Match it only when exactly one paid child has that hash.
func merchantOrders() *gorm.DB {
	rows := dao.Mdb.Table("orders AS merchant").
		Where("merchant.deleted_at IS NULL").
		Where("(merchant.parent_trade_id = '' OR merchant.parent_trade_id IS NULL)").
		Joins(`LEFT JOIN orders AS payment ON payment.deleted_at IS NULL
			AND (payment.id = merchant.id OR (payment.parent_trade_id = merchant.trade_id AND payment.status = ?))
			AND payment.id = CASE
				WHEN merchant.status <> ? THEN merchant.id
				WHEN merchant.pay_by_sub_id > 0 THEN NULLIF(merchant.pay_by_sub_id, merchant.id)
				WHEN merchant.block_transaction_id <> '' THEN COALESCE((
					SELECT MIN(old.id) FROM orders AS old
					WHERE old.parent_trade_id = merchant.trade_id AND old.status = ?
					  AND old.deleted_at IS NULL AND old.block_transaction_id = merchant.block_transaction_id
					HAVING COUNT(*) = 1
				), merchant.id)
				ELSE merchant.id
			END`, mdb.StatusPaySuccess, mdb.StatusPaySuccess, mdb.StatusPaySuccess).
		Select(`merchant.id, merchant.trade_id, merchant.order_id, merchant.parent_trade_id,
			merchant.amount, merchant.currency, merchant.status, merchant.name,
			merchant.notify_url, merchant.redirect_url, merchant.payment_type,
			merchant.callback_num, merchant.callback_confirm, merchant.api_key_id,
			merchant.review_transaction_hash, merchant.created_at, merchant.updated_at, merchant.deleted_at,
			COALESCE(NULLIF(payment.id, merchant.id), 0) AS pay_by_sub_id,
			payment.network, payment.token, payment.receive_address, payment.actual_amount,
			payment.block_transaction_id, payment.pay_provider, payment.is_selected`)
	return dao.Mdb.Table("(?) AS orders", rows)
}

// GetAdminOrder returns the merchant view of a parent, or the original attempt
// when an operator explicitly opens a child trade ID.
func GetAdminOrder(tradeID string) (*mdb.Orders, error) {
	var order mdb.Orders
	err := merchantOrders().Where("trade_id = ?", tradeID).Limit(1).Find(&order).Error
	if err != nil || order.ID != 0 {
		return &order, err
	}
	return GetOrderInfoByTradeId(tradeID)
}
