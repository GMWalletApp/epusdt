package data

import (
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm"
)

// OrderListFilter bundles every filter supported by the admin orders
// list page. Zero values are ignored so callers can pass only what the
// user actually filtered on.
type OrderListFilter struct {
	Statuses []int
	Networks []string
	Token    string
	Address  string
	Keyword  string // matches trade_id / order_id / block_transaction_id
	StartAt  *time.Time
	EndAt    *time.Time
	Page     int
	PageSize int
}

// ListOrders returns a paginated order slice plus the total count under
// the same filter (for the UI pagination bar).
func ListOrders(f OrderListFilter) ([]mdb.Orders, int64, error) {
	tx := buildOrderListQuery(f)
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page := f.Page
	if page < 1 {
		page = 1
	}
	size := f.PageSize
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	var rows []mdb.Orders
	err := tx.Order("id DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&rows).Error
	return rows, total, err
}

func buildOrderListQuery(f OrderListFilter) *gorm.DB {
	tx := merchantOrders()
	if len(f.Statuses) > 0 {
		tx = tx.Where("status IN ?", f.Statuses)
	}
	if len(f.Networks) > 0 {
		tx = tx.Where("network IN ?", f.Networks)
	}
	if f.Token != "" {
		tx = tx.Where("token = ?", strings.ToUpper(f.Token))
	}
	if f.Address != "" {
		tx = tx.Where("receive_address = ?", f.Address)
	}
	if f.Keyword != "" {
		kw := "%" + strings.TrimSpace(f.Keyword) + "%"
		tx = tx.Where(`CAST(id AS TEXT) LIKE ? OR trade_id LIKE ? OR order_id LIKE ? OR block_transaction_id LIKE ?
			OR EXISTS (SELECT 1 FROM orders AS attempt
				WHERE attempt.parent_trade_id = orders.trade_id AND attempt.deleted_at IS NULL
				  AND (attempt.trade_id LIKE ? OR attempt.order_id LIKE ? OR attempt.block_transaction_id LIKE ? OR attempt.review_transaction_hash LIKE ?))`,
			kw, kw, kw, kw, kw, kw, kw, kw)
	}
	if f.StartAt != nil {
		tx = tx.Where("created_at >= ?", *f.StartAt)
	}
	if f.EndAt != nil {
		tx = tx.Where("created_at <= ?", *f.EndAt)
	}
	return tx
}

// OrdersForExport returns the same merchant orders as the paginated list.
func OrdersForExport(f OrderListFilter) ([]mdb.Orders, error) {
	var rows []mdb.Orders
	err := buildOrderListQuery(f).Order("id DESC").Limit(10000).Find(&rows).Error
	return rows, err
}

// CountOrdersByStatus returns how many orders exist in each status.
// Used by the dashboard overview card.
func CountOrdersByStatus() (map[int]int64, error) {
	type row struct {
		Status int
		Total  int64
	}
	var rows []row
	err := merchantOrders().
		Select("status, COUNT(*) AS total").
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[int]int64{}
	for _, r := range rows {
		out[r.Status] = r.Total
	}
	return out, nil
}

// CloseOrderManually transitions a pending order to expired. It covers both
// payable orders and placeholder orders waiting for token/network selection.
func CloseOrderManually(tradeID string) (bool, error) {
	closable := []int{mdb.StatusWaitPay, mdb.StatusWaitSelect, mdb.StatusConfirming, mdb.StatusManualReview}
	var closed bool
	err := dao.Mdb.Transaction(func(tx *gorm.DB) error {
		var order mdb.Orders
		if err := tx.Where("trade_id = ?", tradeID).Limit(1).Find(&order).Error; err != nil {
			return err
		}
		if order.ID == 0 {
			return nil
		}
		result := tx.Model(&mdb.Orders{}).
			Where("id = ?", order.ID).
			Where("status IN ?", closable).
			Updates(map[string]interface{}{
				"status":                  mdb.StatusExpired,
				"review_transaction_hash": "",
			})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		closed = true
		orderIDs := []uint64{order.ID}
		if order.ParentTradeId == "" {
			var subs []mdb.Orders
			if err := tx.Where("parent_trade_id = ? AND status IN ?", order.TradeId, closable).Find(&subs).Error; err != nil {
				return err
			}
			if len(subs) > 0 {
				if err := tx.Model(&mdb.Orders{}).
					Where("parent_trade_id = ? AND status IN ?", order.TradeId, closable).
					Updates(map[string]interface{}{
						"status":                  mdb.StatusExpired,
						"review_transaction_hash": "",
					}).Error; err != nil {
					return err
				}
				for _, s := range subs {
					orderIDs = append(orderIDs, s.ID)
				}
			}
		}
		if !tx.Migrator().HasTable(&mdb.PaymentConfirmation{}) {
			return nil
		}
		return tx.Where("order_id IN ?", orderIDs).Delete(&mdb.PaymentConfirmation{}).Error
	})
	return closed, err
}

// ReopenOrderCallback flips callback_confirm back to NO so the mq
// worker picks it up on the next tick. Used by "resend callback".
func ReopenOrderCallback(tradeID string) (bool, error) {
	result := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Where("status = ?", mdb.StatusPaySuccess).
		Updates(map[string]interface{}{
			"callback_confirm": mdb.CallBackConfirmNo,
			"callback_num":     0,
		})
	return result.RowsAffected > 0, result.Error
}

// CountOrdersByAddress returns order counts grouped by receive_address.
// The admin wallet list annotates each wallet row with this number.
func CountOrdersByAddress() (map[string]int64, error) {
	type row struct {
		Address string `gorm:"column:receive_address"`
		Total   int64
	}
	var rows []row
	err := dao.Mdb.Model(&mdb.Orders{}).
		Select("receive_address, COUNT(*) AS total").
		Group("receive_address").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Address] = r.Total
	}
	return out, nil
}
