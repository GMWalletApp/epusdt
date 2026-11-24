package data

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/GMWalletApp/epusdt/util/constant"
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

func normalizeAmount(amount float64, precision int) (int64, string) {
	precision = NormalizeAmountPrecision(precision)
	value := decimal.NewFromFloat(amount).Round(int32(precision))
	return value.Shift(int32(precision)).IntPart(), value.StringFixed(int32(precision))
}

func normalizeLockAmount(amount float64) (int64, string) {
	return normalizeAmount(amount, GetAmountPrecision())
}

func normalizeLockNetwork(network string) string {
	return strings.ToLower(strings.TrimSpace(network))
}

func normalizeLockAddress(network, address string) string {
	address = strings.TrimSpace(address)
	network = normalizeLockNetwork(network)
	if isEVMNetwork(network) {
		return strings.ToLower(address)
	}
	if network == mdb.NetworkTon {
		if raw, err := addressutil.TonRawAddressKey(address); err == nil {
			return raw
		}
	}
	if network == mdb.NetworkAptos || network == mdb.NetworkSui {
		if normalized, err := addressutil.NormalizeMoveAddress(address); err == nil {
			return normalized
		}
	}
	return address
}

func normalizeLockToken(token string) string {
	return strings.ToUpper(strings.TrimSpace(token))
}

func applyLockAddressFilter(tx *gorm.DB, network, address string) *gorm.DB {
	network = normalizeLockNetwork(network)
	address = normalizeLockAddress(network, address)
	if isEVMNetwork(network) {
		return tx.Where("lower(address) = ?", address)
	}
	return tx.Where("address = ?", address)
}

func applyOrderReceiveAddressFilter(tx *gorm.DB, network, address string) *gorm.DB {
	network = normalizeLockNetwork(network)
	address = normalizeLockAddress(network, address)
	if isEVMNetwork(network) {
		return tx.Where("lower(receive_address) = ?", address)
	}
	return tx.Where("receive_address = ?", address)
}

func activeLocksForAddress(tx *gorm.DB, network, address, token string, now time.Time) *gorm.DB {
	query := tx.Model(&mdb.TransactionLock{}).
		Where("network = ?", normalizeLockNetwork(network)).
		Where("token = ?", normalizeLockToken(token)).
		Where("expires_at > ?", now)
	return applyLockAddressFilter(query, network, address)
}

func lockMatchesAmount(lock mdb.TransactionLock, amount float64) bool {
	precision := NormalizeAmountPrecision(lock.AmountPrecision)
	scaledAmount, _ := normalizeAmount(amount, precision)
	return lock.AmountScaled == scaledAmount
}

func lockAmountDecimal(lock mdb.TransactionLock) decimal.Decimal {
	if strings.TrimSpace(lock.AmountText) != "" {
		if value, err := decimal.NewFromString(lock.AmountText); err == nil {
			return value
		}
	}
	precision := NormalizeAmountPrecision(lock.AmountPrecision)
	return decimal.NewFromInt(lock.AmountScaled).Shift(int32(-precision))
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

// SQLiteTimestampUTCExpr converts supported SQLite timestamp formats to UTC.
func SQLiteTimestampUTCExpr(column string) string {
	plusPos := "instr(" + column + ", ' +')"
	minusPos := "instr(" + column + ", ' -')"
	formatOffset := func(pos string) string {
		return "CASE WHEN substr(" + column + ", " + pos + " + 4, 1) = ':' " +
			"THEN datetime(substr(" + column + ", 1, 19) || substr(" + column + ", " + pos + " + 1, 6)) " +
			"ELSE datetime(substr(" + column + ", 1, 19) || substr(" + column + ", " + pos + " + 1, 3) || ':' || substr(" + column + ", " + pos + " + 4, 2)) END"
	}
	return "CASE " +
		"WHEN " + plusPos + " > 0 THEN " + formatOffset(plusPos) + " " +
		"WHEN " + minusPos + " > 0 THEN " + formatOffset(minusPos) + " " +
		"ELSE datetime(" + column + ") END"
}

var orderCreatedAtUTCExpr = SQLiteTimestampUTCExpr("created_at")

func orderTimeUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
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

// GetOrderByBlockTransactionIDs fetches the first order whose stored tx id
// matches any equivalent spelling supplied by the caller.
func GetOrderByBlockTransactionIDs(blockIDs []string) (*mdb.Orders, error) {
	order := new(mdb.Orders)
	seen := make(map[string]struct{}, len(blockIDs))
	candidates := make([]string, 0, len(blockIDs))
	for _, blockID := range blockIDs {
		blockID = strings.TrimSpace(blockID)
		if blockID == "" {
			continue
		}
		if _, ok := seen[blockID]; ok {
			continue
		}
		seen[blockID] = struct{}{}
		candidates = append(candidates, blockID)
	}
	if len(candidates) == 0 {
		return order, nil
	}
	err := dao.Mdb.Model(order).Where("block_transaction_id IN ?", candidates).Limit(1).Find(order).Error
	return order, err
}

// GetOrderByBlockTransactionIDsCaseInsensitive fetches the first order whose
// stored tx id matches any candidate after ASCII case folding. This is used
// only for hex-based chain tx ids; case-sensitive signatures must keep using
// GetOrderByBlockTransactionIDs.
func GetOrderByBlockTransactionIDsCaseInsensitive(blockIDs []string) (*mdb.Orders, error) {
	order := new(mdb.Orders)
	seen := make(map[string]struct{}, len(blockIDs))
	candidates := make([]string, 0, len(blockIDs))
	for _, blockID := range blockIDs {
		blockID = strings.ToLower(strings.TrimSpace(blockID))
		if blockID == "" {
			continue
		}
		if _, ok := seen[blockID]; ok {
			continue
		}
		seen[blockID] = struct{}{}
		candidates = append(candidates, blockID)
	}
	if len(candidates) == 0 {
		return order, nil
	}
	err := dao.Mdb.Model(order).Where("LOWER(block_transaction_id) IN ?", candidates).Limit(1).Find(order).Error
	return order, err
}

// OrderSuccessWithTransaction marks an order as paid only if it is still waiting for payment.
func OrderSuccessWithTransaction(tx *gorm.DB, req *request.OrderProcessingRequest) (bool, error) {
	return OrderSuccessWithStatusesWithTransaction(tx, req, []int{mdb.StatusWaitPay})
}

// OrderSuccessWithStatusesWithTransaction marks an order as paid only if its
// current status is included in allowedStatuses.
func OrderSuccessWithStatusesWithTransaction(tx *gorm.DB, req *request.OrderProcessingRequest, allowedStatuses []int) (bool, error) {
	if len(allowedStatuses) == 0 {
		return false, nil
	}
	key := sha256.Sum256([]byte(req.BlockTransactionId))
	result := tx.Model(&mdb.Orders{}).
		Where("trade_id = ?", req.TradeId).
		Where("status IN ?", allowedStatuses).
		Updates(map[string]interface{}{
			"review_transaction_hash": "",
			"settlement_key":          hex.EncodeToString(key[:]),
			"block_transaction_id":    req.BlockTransactionId,
			"status":                  mdb.StatusPaySuccess,
			"callback_confirm":        mdb.CallBackConfirmNo,
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
		Where("status IN ?", []int{mdb.StatusWaitPay, mdb.StatusWaitSelect}).
		Where(orderCreatedAtUTCExpr+" <= ?", orderTimeUTC(expirationCutoff)).
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

// CountSubOrders counts all sub-orders ever created under a parent.
func CountSubOrders(parentTradeId string) (int64, error) {
	var count int64
	err := dao.Mdb.Model(&mdb.Orders{}).
		Where("parent_trade_id = ?", parentTradeId).
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

// MarkParentOrderSuccess marks the parent order as paid and records which sub-order
// settled it. Only the status, callback_confirm, and pay_by_sub_id fields are updated;
// the parent's own block_transaction_id, actual_amount, and receive_address are
// intentionally left unchanged because the parent was not directly paid.
func MarkParentOrderSuccess(parentTradeId string, sub *mdb.Orders) (bool, error) {
	return MarkParentOrderSuccessWithTransaction(dao.Mdb, parentTradeId, sub)
}

// MarkParentOrderSuccessWithTransaction is the transactional variant of
// MarkParentOrderSuccess.
func MarkParentOrderSuccessWithTransaction(tx *gorm.DB, parentTradeId string, sub *mdb.Orders) (bool, error) {
	return MarkParentOrderSuccessWithStatusesWithTransaction(tx, parentTradeId, sub, []int{mdb.StatusWaitPay})
}

// MarkParentOrderSuccessWithStatusesWithTransaction marks a parent order as paid
// only if its current status is included in allowedStatuses.
func MarkParentOrderSuccessWithStatusesWithTransaction(tx *gorm.DB, parentTradeId string, sub *mdb.Orders, allowedStatuses []int) (bool, error) {
	if len(allowedStatuses) == 0 {
		return false, nil
	}
	result := tx.Model(&mdb.Orders{}).
		Where("trade_id = ?", parentTradeId).
		Where("status IN ?", allowedStatuses).
		Updates(map[string]interface{}{
			"status":                  mdb.StatusPaySuccess,
			"callback_confirm":        mdb.CallBackConfirmNo,
			"review_transaction_hash": "",
			"pay_by_sub_id":           sub.ID,
		})
	return result.RowsAffected > 0, result.Error
}

// ExpireSiblingSubOrdersWithTransaction expires all waiting sibling
// sub-orders under the same parent in one statement.
func ExpireSiblingSubOrdersWithTransaction(tx *gorm.DB, parentTradeId string, excludeTradeId string) error {
	return tx.Model(&mdb.Orders{}).
		Where("parent_trade_id = ?", parentTradeId).
		Where("trade_id != ?", excludeTradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Update("status", mdb.StatusExpired).Error
}

// SelectCheckoutOrder selects one payable family member atomically. When refreshing
// a reused on-chain child, both it and the parent must retain their reservations.
// Hosted children's deadlines remain controlled by their external provider.
func SelectCheckoutOrder(db *gorm.DB, rootID string, selected *mdb.Orders, renew bool) error {
	var saved mdb.Orders
	err := db.Transaction(func(tx *gorm.DB) error {
		if renew {
			if err := RefreshOrderExpirationWithTransaction(tx, rootID); err != nil {
				return err
			}
			if selected.TradeId != rootID && (selected.PayProvider == "" || selected.PayProvider == mdb.PaymentProviderOnChain) {
				if err := RefreshOrderExpirationWithTransaction(tx, selected.TradeId); err != nil {
					return err
				}
			}
		}
		result := tx.Model(&mdb.Orders{}).Where("trade_id = ? AND status = ?", selected.TradeId, mdb.StatusWaitPay).
			Where("((trade_id = ? AND COALESCE(parent_trade_id, '') = '') OR parent_trade_id = ?)", rootID, rootID).
			Update("is_selected", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return constant.OrderStatusConflict
		}
		if err := tx.Model(&mdb.Orders{}).Where("(trade_id = ? OR parent_trade_id = ?)", rootID, rootID).
			Where("trade_id <> ? AND is_selected = ?", selected.TradeId, true).Update("is_selected", false).Error; err != nil {
			return err
		}
		return tx.Where("trade_id = ?", selected.TradeId).Take(&saved).Error
	})
	if err == nil {
		*selected = saved
	}
	return err
}

// SelectedCheckoutChild also handles older databases where both parent and child
// could be flagged as selected. The next selection reconciles those flags.
func SelectedCheckoutChild(rootID string) (*mdb.Orders, error) {
	var child mdb.Orders
	err := dao.Mdb.Where("parent_trade_id = ? AND is_selected = ?", rootID, true).
		Where("status IN ?", []int{mdb.StatusWaitPay, mdb.StatusConfirming, mdb.StatusManualReview}).
		Order("id DESC").Limit(1).Find(&child).Error
	return &child, err
}

// CompleteWaitSelectOrder turns a waiting placeholder into an on-chain order.
func CompleteWaitSelectOrder(tradeID string, network string, token string, receiveAddress string, actualAmount float64) (bool, error) {
	return CompleteWaitSelectOrderWithTransaction(dao.Mdb, tradeID, network, token, receiveAddress, actualAmount)
}

// CompleteWaitSelectOrderWithTransaction fills a placeholder order with concrete chain fields
// and makes it payable. It only updates status=WaitSelect rows so concurrent
// switch-network attempts cannot overwrite a payable order.
func CompleteWaitSelectOrderWithTransaction(tx *gorm.DB, tradeID string, network string, token string, receiveAddress string, actualAmount float64) (bool, error) {
	result := tx.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Where("status = ?", mdb.StatusWaitSelect).
		Updates(map[string]interface{}{
			"status":          mdb.StatusWaitPay,
			"network":         strings.ToLower(strings.TrimSpace(network)),
			"token":           strings.ToUpper(strings.TrimSpace(token)),
			"receive_address": receiveAddress,
			"actual_amount":   actualAmount,
			"is_selected":     false,
			"created_at":      time.Now(),
		})
	return result.RowsAffected > 0, result.Error
}

// CompleteWaitSelectOkPayOrderWithTransaction converts a placeholder parent
// directly into an OkPay order. It intentionally does not create a local chain
// lock because OkPay owns the hosted payment target.
func CompleteWaitSelectOkPayOrderWithTransaction(tx *gorm.DB, tradeID string, token string, actualAmount float64) (bool, error) {
	result := tx.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Where("status = ?", mdb.StatusWaitSelect).
		Updates(map[string]interface{}{
			"status":          mdb.StatusWaitPay,
			"network":         mdb.PaymentProviderOkPay,
			"token":           strings.ToUpper(strings.TrimSpace(token)),
			"receive_address": "OKPAY",
			"actual_amount":   actualAmount,
			"is_selected":     false,
			"pay_provider":    mdb.PaymentProviderOkPay,
			"created_at":      time.Now(),
		})
	return result.RowsAffected > 0, result.Error
}

// CompleteWaitSelectXLayerOrderWithTransaction converts a placeholder parent
// directly into an X Layer order. It intentionally does not create a local
// chain lock because the provider owns the hosted payment target, and the
// receive address stays empty so the cashier renders the hosted panel
// (payment_url set, no address) instead of the on-chain address panel.
func CompleteWaitSelectXLayerOrderWithTransaction(tx *gorm.DB, tradeID string, token string, actualAmount float64) (bool, error) {
	result := tx.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Where("status = ?", mdb.StatusWaitSelect).
		Updates(map[string]interface{}{
			"status":          mdb.StatusWaitPay,
			"network":         mdb.PaymentProviderXLayer,
			"token":           strings.ToUpper(strings.TrimSpace(token)),
			"receive_address": "",
			"actual_amount":   actualAmount,
			"is_selected":     false,
			"pay_provider":    mdb.PaymentProviderXLayer,
			"created_at":      time.Now(),
		})
	return result.RowsAffected > 0, result.Error
}

// ExpireOrderByTradeID marks a waiting order as expired. Used to retire failed
// child-order attempts that should not remain selectable/reusable.
func ExpireOrderByTradeID(tradeId string) error {
	return dao.Mdb.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeId).
		Where("status = ?", mdb.StatusWaitPay).
		Updates(map[string]interface{}{
			"status":      mdb.StatusExpired,
			"is_selected": false,
		}).Error
}

// RefreshOrderExpiration extends a pending order only after its reservation is renewed.
func RefreshOrderExpiration(tradeId string) error {
	return dao.Mdb.Transaction(func(tx *gorm.DB) error {
		return RefreshOrderExpirationWithTransaction(tx, tradeId)
	})
}

func RefreshOrderExpirationWithTransaction(tx *gorm.DB, tradeID string) error {
	updated, err := refreshOrderExpirationWithTransaction(tx, tradeID, map[string]interface{}{})
	if err != nil {
		return err
	}
	if !updated {
		return constant.OrderStatusConflict
	}
	return nil
}

func refreshOrderExpirationWithTransaction(tx *gorm.DB, tradeID string, updates map[string]interface{}) (bool, error) {
	var order mdb.Orders
	if err := tx.Where("trade_id = ?", tradeID).Limit(1).Find(&order).Error; err != nil {
		return false, err
	}
	now := time.Now()
	expiration := config.GetOrderExpirationTimeDuration()
	if order.ID == 0 ||
		(order.Status != mdb.StatusWaitPay && order.Status != mdb.StatusWaitSelect) ||
		!order.CreatedAt.StdTime().Add(expiration).After(now) {
		return false, nil
	}

	provider := strings.TrimSpace(order.PayProvider)
	if order.Status == mdb.StatusWaitPay && (provider == "" || provider == mdb.PaymentProviderOnChain) {
		// Commit the runtime extension first. If the main transaction fails,
		// retaining a reservation too long is safer than exposing a payable target.
		renewed, err := renewOrderTransactionLock(&order, now, now.Add(expiration))
		if err != nil {
			return false, err
		}
		if !renewed {
			return false, nil
		}
	}

	updates["created_at"] = now.UTC()
	result := tx.Model(&mdb.Orders{}).
		Where("trade_id = ?", tradeID).
		Where("status = ?", order.Status).
		Where(orderCreatedAtUTCExpr+" > ?", orderTimeUTC(now.Add(-expiration))).
		Updates(updates)
	return result.RowsAffected > 0, result.Error
}

func ExtendOrderTransactionLock(order *mdb.Orders, expiresAt time.Time) error {
	if order == nil || strings.TrimSpace(order.TradeId) == "" {
		return nil
	}
	now := time.Now()
	renewed, err := renewOrderTransactionLock(order, now, expiresAt)
	if err != nil || renewed {
		return err
	}
	err = LockTransaction(order.Network, order.ReceiveAddress, order.Token, order.TradeId, order.ActualAmount, expiresAt.Sub(now))
	if errors.Is(err, ErrTransactionLocked) {
		return nil
	}
	return err
}

func renewOrderTransactionLock(order *mdb.Orders, now, expiresAt time.Time) (bool, error) {
	var locks []mdb.TransactionLock
	if err := activeLocksForAddress(dao.RuntimeDB, order.Network, order.ReceiveAddress, order.Token, now).
		Where("trade_id = ?", order.TradeId).Find(&locks).Error; err != nil {
		return false, err
	}
	for _, lock := range locks {
		// Keep the stored precision; changing settings must not change this target.
		if !lockAmountDecimal(lock).Equal(decimal.NewFromFloat(order.ActualAmount)) {
			continue
		}
		result := dao.RuntimeDB.Model(&mdb.TransactionLock{}).
			Where("id = ? AND trade_id = ? AND expires_at > ?", lock.ID, order.TradeId, time.Now()).
			Update("expires_at", gorm.Expr("CASE WHEN expires_at < ? THEN ? ELSE expires_at END", expiresAt, expiresAt))
		return result.RowsAffected > 0, result.Error
	}
	return false, nil
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

// GetAllSubOrders returns all sub-orders (any status) under a parent order,
// ordered by creation time ascending.
func GetAllSubOrders(parentTradeId string) ([]mdb.Orders, error) {
	var orders []mdb.Orders
	err := dao.Mdb.Model(&mdb.Orders{}).
		Where("parent_trade_id = ?", parentTradeId).
		Order("created_at ASC").
		Find(&orders).Error
	return orders, err
}

// GetSubOrdersByParentTradeIds batch-fetches all sub-orders (any status)
// whose parent_trade_id is in the given set. Ordered by parent_trade_id
// then created_at ASC so callers can build a grouped map in one pass.
func GetSubOrdersByParentTradeIds(parentTradeIds []string) ([]mdb.Orders, error) {
	if len(parentTradeIds) == 0 {
		return nil, nil
	}
	var orders []mdb.Orders
	err := dao.Mdb.Model(&mdb.Orders{}).
		Where("parent_trade_id IN ?", parentTradeIds).
		Order("parent_trade_id ASC, created_at ASC").
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
	network = normalizeLockNetwork(network)
	address = normalizeLockAddress(network, address)
	var locks []mdb.TransactionLock
	err := activeLocksForAddress(dao.RuntimeDB, network, address, token, time.Now()).
		Order("created_at ASC").
		Find(&locks).Error
	if err != nil {
		return "", err
	}
	for _, lock := range locks {
		if lockMatchesAmount(lock, amount) {
			return lock.TradeId, nil
		}
	}
	return "", nil
}

// GetOnChainOrderByWalletAddressAndAmountAndTokenBeforeTime finds the most
// relevant on-chain order row when the runtime lock is gone. It is used by
// backfill scanners that need to recover a payment after the reservation lock
// has already expired.
func GetOnChainOrderByWalletAddressAndAmountAndTokenBeforeTime(network string, address string, token string, amount float64, before time.Time) (*mdb.Orders, error) {
	network = normalizeLockNetwork(network)
	address = normalizeLockAddress(network, address)
	token = normalizeLockToken(token)

	order := new(mdb.Orders)
	query := dao.Mdb.Model(order).
		Where("network = ?", network).
		Where("token = ?", token).
		Where("actual_amount = ?", amount).
		Where(orderCreatedAtUTCExpr+" <= ?", orderTimeUTC(before)).
		Where("status IN ?", []int{mdb.StatusWaitPay, mdb.StatusExpired, mdb.StatusConfirming}).
		Where("(pay_provider = ? OR pay_provider = '')", mdb.PaymentProviderOnChain)
	query = applyOrderReceiveAddressFilter(query, network, address)
	err := query.Order("created_at desc, id desc").Limit(1).Find(order).Error
	return order, err
}

// LockTransaction reserves a network+address+token+amount pair in sqlite until expiration.
func LockTransaction(network, address, token, tradeID string, amount float64, expirationTime time.Duration) error {
	network = normalizeLockNetwork(network)
	address = normalizeLockAddress(network, address)
	precision := GetAmountPrecision()
	scaledAmount, amountText := normalizeAmount(amount, precision)
	normalizedToken := normalizeLockToken(token)
	now := time.Now()
	lock := &mdb.TransactionLock{
		Network:         network,
		Address:         address,
		Token:           normalizedToken,
		AmountScaled:    scaledAmount,
		AmountText:      amountText,
		AmountPrecision: precision,
		TradeId:         tradeID,
		ExpiresAt:       now.Add(expirationTime),
	}

	return dao.RuntimeDB.Transaction(func(tx *gorm.DB) error {
		expiredQuery := tx.Where("network = ?", network).
			Where("token = ?", normalizedToken).
			Where("expires_at <= ?", now)
		expiredQuery = applyLockAddressFilter(expiredQuery, network, address)
		if err := expiredQuery.Delete(&mdb.TransactionLock{}).Error; err != nil {
			return err
		}
		if err := tx.Where("trade_id = ?", tradeID).Delete(&mdb.TransactionLock{}).Error; err != nil {
			return err
		}

		var existing []mdb.TransactionLock
		if err := activeLocksForAddress(tx, network, address, normalizedToken, now).
			Find(&existing).Error; err != nil {
			return err
		}
		candidateAmount := lockAmountDecimal(*lock)
		for _, item := range existing {
			if lockAmountDecimal(item).Equal(candidateAmount) {
				return ErrTransactionLocked
			}
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

// UnLockTransactionByTradeId releases only reservations owned by this order,
// including expired rows. Never release a reusable target without its owner.
func UnLockTransactionByTradeId(tradeID string) error {
	return dao.RuntimeDB.Where("trade_id = ?", tradeID).Delete(&mdb.TransactionLock{}).Error
}

func CleanupExpiredTransactionLocks() error {
	return dao.RuntimeDB.Where("expires_at <= ?", time.Now()).Delete(&mdb.TransactionLock{}).Error
}
