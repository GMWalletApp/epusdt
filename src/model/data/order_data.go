package data

import (
	"errors"
	"strings"
	"time"

	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/request"
	"github.com/dromara/carbon/v2"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrTransactionLocked = errors.New("transaction amount is already locked")

type PendingCallbackOrder struct {
	TradeId         string      `gorm:"column:trade_id"`
	CallbackNum     int         `gorm:"column:callback_num"`
	CallBackConfirm int         `gorm:"column:callback_confirm"`
	UpdatedAt       carbon.Time `gorm:"column:updated_at"`
}

func normalizeLockAmount(amount float64) (int64, string) {
	value := decimal.NewFromFloat(amount).Round(2)
	return value.Shift(2).IntPart(), value.StringFixed(2)
}

func normalizeLockToken(token string) string {
	return strings.ToUpper(strings.TrimSpace(token))
}

// GetOrderInfoByOrderId fetches an order by merchant order id.
func GetOrderInfoByOrderId(orderId string) (*mdb.Orders, error) {
	order := new(mdb.Orders)
	err := dao.Mdb.Model(order).Limit(1).Find(order, "order_id = ?", orderId).Error
	return order, err
}

// GetOrderInfoByTradeId fetches an order by epusdt trade id.
func GetOrderInfoByTradeId(tradeId string) (*mdb.Orders, error) {
	order := new(mdb.Orders)
	err := dao.Mdb.Model(order).Limit(1).Find(order, "trade_id = ?", tradeId).Error
	return order, err
}

// CreateOrderWithTransaction creates an order in the active database transaction.
func CreateOrderWithTransaction(tx *gorm.DB, order *mdb.Orders) error {
	return tx.Model(order).Create(order).Error
}

// GetOrderByBlockIdWithTransaction fetches an order by blockchain tx id.
func GetOrderByBlockIdWithTransaction(tx *gorm.DB, blockID string) (*mdb.Orders, error) {
	order := new(mdb.Orders)
	err := tx.Model(order).Limit(1).Find(order, "block_transaction_id = ?", blockID).Error
	return order, err
}

// OrderSuccessWithTransaction marks an order as paid only if it is still waiting for payment.
func OrderSuccessWithTransaction(tx *gorm.DB, req *request.OrderProcessingRequest) (bool, error) {
	result := tx.Model(&mdb.Orders{}).
		Where("trade_id = ?", req.TradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Updates(map[string]interface{}{
			"block_transaction_id": req.BlockTransactionId,
			"status":               mdb.StatusPaySuccess,
			"callback_confirm":     mdb.CallBackConfirmNo,
		})
	return result.RowsAffected > 0, result.Error
}

// GetPendingCallbackOrders returns the minimal callback scheduling state.
func GetPendingCallbackOrders(maxRetry int, limit int) ([]PendingCallbackOrder, error) {
	var orders []PendingCallbackOrder
	query := dao.Mdb.Model(&mdb.Orders{}).
		Select("trade_id", "callback_num", "callback_confirm", "updated_at").
		Where("callback_num <= ?", maxRetry).
		Where("callback_confirm = ?", mdb.CallBackConfirmNo).
		Where("status = ?", mdb.StatusPaySuccess).
		Order("updated_at asc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&orders).Error
	return orders, err
}

// SaveCallBackOrdersResp persists a callback attempt result.
func SaveCallBackOrdersResp(order *mdb.Orders) error {
	return dao.Mdb.Model(order).
		Where("id = ?", order.ID).
		Where("callback_confirm = ?", mdb.CallBackConfirmNo).
		Updates(map[string]interface{}{
			"callback_num":     gorm.Expr("callback_num + ?", 1),
			"callback_confirm": order.CallBackConfirm,
		}).Error
}

// UpdateOrderIsExpirationById expires an order only if it is still pending and already timed out.
func UpdateOrderIsExpirationById(id uint64, expirationCutoff time.Time) (bool, error) {
	result := dao.Mdb.Model(mdb.Orders{}).
		Where("id = ?", id).
		Where("status = ?", mdb.StatusWaitPay).
		Where("created_at <= ?", expirationCutoff).
		Update("status", mdb.StatusExpired)
	return result.RowsAffected > 0, result.Error
}

// CountActiveSubOrders counts sub-orders with status=WaitPay under a parent.
func CountActiveSubOrders(parentTradeId string) (int64, error) {
	var count int64
	err := dao.Mdb.Model(&mdb.Orders{}).
		Where("parent_trade_id = ?", parentTradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Count(&count).Error
	return count, err
}

// GetSubOrderByTokenNetwork finds an existing active sub-order matching token+network under a parent.
func GetSubOrderByTokenNetwork(parentTradeId string, token string, network string) (*mdb.Orders, error) {
	order := new(mdb.Orders)
	err := dao.Mdb.Model(order).
		Where("parent_trade_id = ?", parentTradeId).
		Where("token = ?", token).
		Where("network = ?", network).
		Where("status = ?", mdb.StatusWaitPay).
		Limit(1).
		Find(order).Error
	return order, err
}

// GetSiblingSubOrders returns active sub-orders under the same parent, excluding the given trade_id.
func GetSiblingSubOrders(parentTradeId string, excludeTradeId string) ([]mdb.Orders, error) {
	var orders []mdb.Orders
	err := dao.Mdb.Model(&mdb.Orders{}).
		Where("parent_trade_id = ?", parentTradeId).
		Where("trade_id != ?", excludeTradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Find(&orders).Error
	return orders, err
}

// MarkParentOrderSuccess updates the parent order with the actual paid sub-order details.
func MarkParentOrderSuccess(parentTradeId string, sub *mdb.Orders) (bool, error) {
	result := dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", parentTradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Updates(map[string]interface{}{
			"status":               mdb.StatusPaySuccess,
			"block_transaction_id": sub.BlockTransactionId,
			"callback_confirm":     mdb.CallBackConfirmNo,
			"actual_amount":        sub.ActualAmount,
			"receive_address":      sub.ReceiveAddress,
			"token":                sub.Token,
			"network":              sub.Network,
		})
	return result.RowsAffected > 0, result.Error
}

func SetSelectedOrder(rootTradeId string, selectedTradeId string) error {
	return dao.Mdb.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&mdb.Orders{}).
			Where("trade_id = ? OR parent_trade_id = ?", rootTradeId, rootTradeId).
			Update("is_selected", false).Error; err != nil {
			return err
		}
		return tx.Model(&mdb.Orders{}).
			Where("trade_id = ?", selectedTradeId).
			Update("is_selected", true).Error
	})
}

func GetSelectedOrderInFamily(rootTradeId string) (*mdb.Orders, error) {
	order := new(mdb.Orders)
	err := dao.Mdb.Model(order).
		Where("(trade_id = ? OR parent_trade_id = ?)", rootTradeId, rootTradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Where("is_selected = ?", true).
		Order("parent_trade_id asc, id asc").
		Limit(1).
		Find(order).Error
	return order, err
}

func GetActiveOrdersInFamily(rootTradeId string) ([]mdb.Orders, error) {
	var orders []mdb.Orders
	err := dao.Mdb.Model(&mdb.Orders{}).
		Where("(trade_id = ? OR parent_trade_id = ?)", rootTradeId, rootTradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Find(&orders).Error
	return orders, err
}

func RefreshOrderFamilyExpiration(rootTradeId string, expirationTime time.Duration) error {
	now := time.Now()
	orders, err := GetActiveOrdersInFamily(rootTradeId)
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		return nil
	}
	tradeIDs := make([]string, 0, len(orders))
	for _, order := range orders {
		tradeIDs = append(tradeIDs, order.TradeId)
	}
	if err = dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id IN ?", tradeIDs).
		Update("created_at", now).Error; err != nil {
		return err
	}
	return dao.RuntimeDB.Model(&mdb.TransactionLock{}).
		Where("trade_id IN ?", tradeIDs).
		Update("expires_at", now.Add(expirationTime)).Error
}

// ResetCallbackConfirmOk sets callback_confirm back to Ok.
// Prevents the callback worker from retrying a sub-order with an empty notify_url.
func ResetCallbackConfirmOk(tradeId string) error {
	return dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeId).
		Update("callback_confirm", mdb.CallBackConfirmOk).Error
}

// GetActiveSubOrders returns all active sub-orders under a parent.
func GetActiveSubOrders(parentTradeId string) ([]mdb.Orders, error) {
	var orders []mdb.Orders
	err := dao.Mdb.Model(&mdb.Orders{}).
		Where("parent_trade_id = ?", parentTradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Find(&orders).Error
	return orders, err
}

// ExpireOrderByTradeId marks a single order as expired if still waiting.
func ExpireOrderByTradeId(tradeId string) error {
	return dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Update("status", mdb.StatusExpired).Error
}

// GetTradeIdByWalletAddressAndAmountAndToken resolves the reserved trade id by network, address, token and amount.
func GetTradeIdByWalletAddressAndAmountAndToken(network string, address string, token string, amount float64) (string, error) {
	scaledAmount, _ := normalizeLockAmount(amount)
	var lock mdb.TransactionLock
	err := dao.RuntimeDB.Model(&mdb.TransactionLock{}).
		Where("network = ?", network).
		Where("address = ?", address).
		Where("token = ?", normalizeLockToken(token)).
		Where("amount_scaled = ?", scaledAmount).
		Where("expires_at > ?", time.Now()).
		Limit(1).
		Find(&lock).Error
	if err != nil {
		return "", err
	}
	if lock.ID <= 0 {
		return "", nil
	}
	return lock.TradeId, nil
}

// LockTransaction reserves a network+address+token+amount pair in sqlite until expiration.
func LockTransaction(network, address, token, tradeID string, amount float64, expirationTime time.Duration) error {
	scaledAmount, amountText := normalizeLockAmount(amount)
	normalizedToken := normalizeLockToken(token)
	now := time.Now()
	lock := &mdb.TransactionLock{
		Network:      network,
		Address:      address,
		Token:        normalizedToken,
		AmountScaled: scaledAmount,
		AmountText:   amountText,
		TradeId:      tradeID,
		ExpiresAt:    now.Add(expirationTime),
	}

	return dao.RuntimeDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("network = ?", network).
			Where("address = ?", address).
			Where("token = ?", normalizedToken).
			Where("amount_scaled = ?", scaledAmount).
			Where("expires_at <= ?", now).
			Delete(&mdb.TransactionLock{}).Error; err != nil {
			return err
		}
		if err := tx.Where("trade_id = ?", tradeID).Delete(&mdb.TransactionLock{}).Error; err != nil {
			return err
		}

		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(lock)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTransactionLocked
		}
		return nil
	})
}

// UnLockTransaction releases the reservation for network+address+token+amount.
func UnLockTransaction(network string, address string, token string, amount float64) error {
	scaledAmount, _ := normalizeLockAmount(amount)
	return dao.RuntimeDB.
		Where("network = ?", network).
		Where("address = ?", address).
		Where("token = ?", normalizeLockToken(token)).
		Where("amount_scaled = ?", scaledAmount).
		Delete(&mdb.TransactionLock{}).Error
}

func UnLockTransactionByTradeId(tradeID string) error {
	return dao.RuntimeDB.Where("trade_id = ?", tradeID).Delete(&mdb.TransactionLock{}).Error
}

func CleanupExpiredTransactionLocks() error {
	return dao.RuntimeDB.Where("expires_at <= ?", time.Now()).Delete(&mdb.TransactionLock{}).Error
}
