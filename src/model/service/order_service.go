package service

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/assimon/luuu/config"
	"github.com/assimon/luuu/model/dao"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/request"
	"github.com/assimon/luuu/model/response"
	"github.com/assimon/luuu/util/constant"
	"github.com/assimon/luuu/util/log"
	"github.com/assimon/luuu/util/math"
	"github.com/dromara/carbon/v2"
	"github.com/shopspring/decimal"
)

const (
	CnyMinimumPaymentAmount  = 0.01
	UsdtMinimumPaymentAmount = 0.01
	UsdtAmountPerIncrement   = 0.01
	IncrementalMaximumNumber = 100
)

var (
	gCreateTransactionLock sync.Mutex
	gOrderProcessingLock   sync.Mutex
)

// CreateTransaction creates a new payment order.
func CreateTransaction(req *request.CreateTransactionRequest) (*response.CreateTransactionResponse, error) {
	gCreateTransactionLock.Lock()
	defer gCreateTransactionLock.Unlock()

	token := strings.ToUpper(strings.TrimSpace(req.Token))
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	network := strings.ToLower(strings.TrimSpace(req.Network))
	payAmount := math.MustParsePrecFloat64(req.Amount, 2)
	rate := config.GetRateForCoin(strings.ToLower(token), strings.ToLower(currency))
	if rate <= 0 {
		return nil, constant.RateAmountErr
	}

	decimalPayAmount := decimal.NewFromFloat(payAmount)
	decimalTokenAmount := decimalPayAmount.Mul(decimal.NewFromFloat(rate))
	if decimalPayAmount.Cmp(decimal.NewFromFloat(CnyMinimumPaymentAmount)) == -1 {
		return nil, constant.PayAmountErr
	}
	if decimalTokenAmount.Cmp(decimal.NewFromFloat(UsdtMinimumPaymentAmount)) == -1 {
		return nil, constant.PayAmountErr
	}

	exist, err := data.GetOrderInfoByOrderId(req.OrderId)
	if err != nil {
		return nil, err
	}
	if exist.ID > 0 {
		return nil, constant.OrderAlreadyExists
	}
	if err = ensurePaymentMethodAvailable(network, token); err != nil {
		return nil, err
	}

	walletAddress, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		return nil, err
	}
	if len(walletAddress) <= 0 {
		return nil, constant.NotAvailableWalletAddress
	}

	tradeID := GenerateCode()
	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), 2)
	availableAddress, availableAmount, err := ReserveAvailableWalletAndAmount(tradeID, network, token, amount, walletAddress)
	if err != nil {
		return nil, err
	}
	if availableAddress == "" {
		return nil, constant.NotAvailableAmountErr
	}

	tx := dao.Mdb.Begin()
	order := &mdb.Orders{
		TradeId:           tradeID,
		OrderId:           req.OrderId,
		Amount:            payAmount,
		Currency:          currency,
		ActualAmount:      availableAmount,
		ReceiveAddress:    availableAddress,
		Token:             token,
		Network:           network,
		Status:            mdb.StatusWaitPay,
		NotifyUrl:         req.NotifyUrl,
		RedirectUrl:       req.RedirectUrl,
		Name:              req.Name,
		PaymentType:       req.PaymentType,
		PaymentChannel:    strings.TrimSpace(req.PaymentChannel),
		PaymentMerchantId: strings.TrimSpace(req.PaymentMerchantId),
	}
	if err = data.CreateOrderWithTransaction(tx, order); err != nil {
		tx.Rollback()
		_ = data.UnLockTransactionByTradeId(tradeID)
		return nil, err
	}
	if err = tx.Commit().Error; err != nil {
		tx.Rollback()
		_ = data.UnLockTransactionByTradeId(tradeID)
		return nil, err
	}

	expirationTime := carbon.Now().AddMinutes(config.GetOrderExpirationTime()).Timestamp()
	resp := &response.CreateTransactionResponse{
		TradeId:        order.TradeId,
		OrderId:        order.OrderId,
		Amount:         order.Amount,
		Currency:       order.Currency,
		ActualAmount:   order.ActualAmount,
		ReceiveAddress: order.ReceiveAddress,
		Token:          order.Token,
		ExpirationTime: expirationTime,
		PaymentUrl:     fmt.Sprintf("%s/pay/checkout-counter/%s", config.GetAppUri(), order.TradeId),
	}
	return resp, nil
}

// OrderProcessing marks an order as paid and releases its sqlite reservation.
func OrderProcessing(req *request.OrderProcessingRequest) error {
	gOrderProcessingLock.Lock()
	defer gOrderProcessingLock.Unlock()

	tx := dao.Mdb.Begin()
	exist, err := data.GetOrderByBlockIdWithTransaction(tx, req.BlockTransactionId)
	if err != nil {
		tx.Rollback()
		return err
	}
	if exist.ID > 0 {
		tx.Rollback()
		return constant.OrderBlockAlreadyProcess
	}

	updated, err := data.OrderSuccessWithTransaction(tx, req)
	if err != nil {
		tx.Rollback()
		return err
	}
	if !updated {
		tx.Rollback()
		return constant.OrderStatusConflict
	}
	if err = tx.Commit().Error; err != nil {
		tx.Rollback()
		return err
	}

	if err = data.UnLockTransaction(req.Network, req.ReceiveAddress, req.Token, req.Amount); err != nil {
		log.Sugar.Warnf("[order] unlock transaction after pay success failed, trade_id=%s, err=%v", req.TradeId, err)
	}

	// Load order to check parent-child relationship
	order, err := data.GetOrderInfoByTradeId(req.TradeId)
	if err != nil {
		return nil
	}

	// Parent order paid directly: expire all sub-orders and release their locks
	if order.ParentTradeId == "" {
		subs, subErr := data.GetActiveSubOrders(order.TradeId)
		if subErr != nil {
			log.Sugar.Errorf("[order] get sub-orders for parent failed, trade_id=%s, err=%v", order.TradeId, subErr)
			return nil
		}
		for _, sub := range subs {
			if err = data.ExpireOrderByTradeId(sub.TradeId); err != nil {
				log.Sugar.Warnf("[order] expire sub-order failed, trade_id=%s, err=%v", sub.TradeId, err)
			}
			if err = data.UnLockTransaction(sub.Network, sub.ReceiveAddress, sub.Token, sub.ActualAmount); err != nil {
				log.Sugar.Warnf("[order] unlock sub-order transaction failed, trade_id=%s, err=%v", sub.TradeId, err)
			}
		}
		return nil
	}

	// Sub-order should not trigger its own callback (notify_url is empty).
	// OrderSuccessWithTransaction unconditionally sets callback_confirm=No,
	// reset it here to prevent the callback worker from retrying an empty URL.
	if err = data.ResetCallbackConfirmOk(order.TradeId); err != nil {
		log.Sugar.Warnf("[order] reset sub-order callback_confirm failed, trade_id=%s, err=%v", order.TradeId, err)
	}

	parent, err := data.GetOrderInfoByTradeId(order.ParentTradeId)
	if err != nil {
		log.Sugar.Errorf("[order] load parent order failed, parent_trade_id=%s, err=%v", order.ParentTradeId, err)
		return nil
	}

	// Mark parent as paid with sub-order's payment details
	if _, err = data.MarkParentOrderSuccess(parent.TradeId, order); err != nil {
		log.Sugar.Errorf("[order] mark parent success failed, parent_trade_id=%s, err=%v", parent.TradeId, err)
	}

	// Release parent's own wallet lock
	if err = data.UnLockTransaction(parent.Network, parent.ReceiveAddress, parent.Token, parent.ActualAmount); err != nil {
		log.Sugar.Warnf("[order] unlock parent transaction failed, parent_trade_id=%s, err=%v", parent.TradeId, err)
	}

	// Expire sibling sub-orders and release their locks
	siblings, err := data.GetSiblingSubOrders(parent.TradeId, order.TradeId)
	if err != nil {
		log.Sugar.Errorf("[order] get sibling sub-orders failed, parent_trade_id=%s, err=%v", parent.TradeId, err)
		return nil
	}
	for _, sib := range siblings {
		if err = data.ExpireOrderByTradeId(sib.TradeId); err != nil {
			log.Sugar.Warnf("[order] expire sibling failed, trade_id=%s, err=%v", sib.TradeId, err)
		}
		if err = data.UnLockTransaction(sib.Network, sib.ReceiveAddress, sib.Token, sib.ActualAmount); err != nil {
			log.Sugar.Warnf("[order] unlock sibling transaction failed, trade_id=%s, err=%v", sib.TradeId, err)
		}
	}

	return nil
}

// ReserveAvailableWalletAndAmount finds and locks a network+address+token+amount pair.
func ReserveAvailableWalletAndAmount(tradeID string, network string, token string, amount float64, walletAddress []mdb.WalletAddress) (string, float64, error) {
	availableAddress := ""
	availableAmount := amount

	tryLockWalletFunc := func(targetAmount float64) (string, error) {
		for _, address := range walletAddress {
			err := data.LockTransaction(network, address.Address, token, tradeID, targetAmount, config.GetOrderExpirationTimeDuration())
			if err == nil {
				return address.Address, nil
			}
			if errors.Is(err, data.ErrTransactionLocked) {
				continue
			}
			return "", err
		}
		return "", nil
	}

	for i := 0; i < IncrementalMaximumNumber; i++ {
		address, err := tryLockWalletFunc(availableAmount)
		if err != nil {
			return "", 0, err
		}
		if address == "" {
			decimalOldAmount := decimal.NewFromFloat(availableAmount)
			decimalIncr := decimal.NewFromFloat(UsdtAmountPerIncrement)
			availableAmount = decimalOldAmount.Add(decimalIncr).InexactFloat64()
			continue
		}
		availableAddress = address
		break
	}
	return availableAddress, availableAmount, nil
}

// GenerateCode creates a unique trade id.
func GenerateCode() string {
	date := time.Now().Format("20060102")
	r := rand.Intn(1000)
	return fmt.Sprintf("%s%d%03d", date, time.Now().UnixNano()/1e6, r)
}

// GetOrderInfoByTradeId returns a validated order.
func GetOrderInfoByTradeId(tradeId string) (*mdb.Orders, error) {
	order, err := data.GetOrderInfoByTradeId(tradeId)
	if err != nil {
		return nil, err
	}
	if order.ID <= 0 {
		return nil, constant.OrderNotExists
	}
	return order, nil
}

const MaxSubOrders = 2

// SwitchNetwork creates or returns an existing sub-order for a different token+network.
func SwitchNetwork(req *request.SwitchNetworkRequest) (*response.CheckoutCounterResponse, error) {
	gCreateTransactionLock.Lock()
	defer gCreateTransactionLock.Unlock()

	token := strings.ToUpper(strings.TrimSpace(req.Token))
	network := strings.ToLower(strings.TrimSpace(req.Network))

	// 1. Load parent order
	parent, err := data.GetOrderInfoByTradeId(req.TradeId)
	if err != nil {
		return nil, err
	}
	if parent.ID <= 0 {
		return nil, constant.OrderNotExists
	}
	if parent.ParentTradeId != "" {
		return nil, constant.CannotSwitchSubOrder
	}
	if parent.Status != mdb.StatusWaitPay {
		return nil, constant.OrderNotWaitPay
	}

	// 2. Same token+network as parent → mark selected and return
	if strings.EqualFold(parent.Token, token) && strings.EqualFold(parent.Network, network) {
		if err = data.SetSelectedOrder(parent.TradeId, parent.TradeId); err != nil {
			return nil, err
		}
		if err = data.RefreshOrderFamilyExpiration(parent.TradeId, config.GetOrderExpirationTimeDuration()); err != nil {
			return nil, err
		}
		parent, err = data.GetOrderInfoByTradeId(parent.TradeId)
		if err != nil {
			return nil, err
		}
		parent.IsSelected = true
		return buildCheckoutResponse(parent)
	}

	// 3. Existing active sub-order for this token+network → return it
	existing, err := data.GetSubOrderByTokenNetwork(parent.TradeId, token, network)
	if err != nil {
		return nil, err
	}
	if existing.ID > 0 {
		if err = data.SetSelectedOrder(parent.TradeId, existing.TradeId); err != nil {
			return nil, err
		}
		if err = data.RefreshOrderFamilyExpiration(parent.TradeId, config.GetOrderExpirationTimeDuration()); err != nil {
			return nil, err
		}
		existing, err = data.GetOrderInfoByTradeId(existing.TradeId)
		if err != nil {
			return nil, err
		}
		existing.IsSelected = true
		return buildCheckoutResponse(existing)
	}

	// 4. Check sub-order limit
	count, err := data.CountActiveSubOrders(parent.TradeId)
	if err != nil {
		return nil, err
	}
	if count >= MaxSubOrders {
		return nil, constant.SubOrderLimitExceeded
	}
	if err = ensurePaymentMethodAvailable(network, token); err != nil {
		return nil, err
	}

	// 5. Calculate amount for the new network
	rate := config.GetRateForCoin(strings.ToLower(token), strings.ToLower(parent.Currency))
	if rate <= 0 {
		return nil, constant.RateAmountErr
	}
	decimalPayAmount := decimal.NewFromFloat(parent.Amount)
	decimalTokenAmount := decimalPayAmount.Mul(decimal.NewFromFloat(rate))
	if decimalTokenAmount.Cmp(decimal.NewFromFloat(UsdtMinimumPaymentAmount)) == -1 {
		return nil, constant.PayAmountErr
	}

	// 6. Find and lock wallet
	walletAddress, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		return nil, err
	}
	if len(walletAddress) <= 0 {
		return nil, constant.NotAvailableWalletAddress
	}

	subTradeID := GenerateCode()
	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), 2)
	availableAddress, availableAmount, err := ReserveAvailableWalletAndAmount(subTradeID, network, token, amount, walletAddress)
	if err != nil {
		return nil, err
	}
	if availableAddress == "" {
		return nil, constant.NotAvailableAmountErr
	}

	// 7. Create sub-order
	tx := dao.Mdb.Begin()
	subOrder := &mdb.Orders{
		TradeId:           subTradeID,
		OrderId:           subTradeID, // sub-order uses its own trade_id as order_id (unique constraint)
		ParentTradeId:     parent.TradeId,
		Amount:            parent.Amount,
		Currency:          parent.Currency,
		ActualAmount:      availableAmount,
		ReceiveAddress:    availableAddress,
		Token:             token,
		Network:           network,
		Status:            mdb.StatusWaitPay,
		IsSelected:        true,
		NotifyUrl:         "",
		RedirectUrl:       parent.RedirectUrl,
		Name:              parent.Name,
		CallBackConfirm:   mdb.CallBackConfirmOk, // don't trigger callback on sub-order
		PaymentType:       parent.PaymentType,
		PaymentChannel:    parent.PaymentChannel,
		PaymentMerchantId: parent.PaymentMerchantId,
	}
	if err = data.CreateOrderWithTransaction(tx, subOrder); err != nil {
		tx.Rollback()
		_ = data.UnLockTransactionByTradeId(subTradeID)
		return nil, err
	}
	if err = tx.Commit().Error; err != nil {
		tx.Rollback()
		_ = data.UnLockTransactionByTradeId(subTradeID)
		return nil, err
	}

	// Mark parent as selected and refresh its expiration to match the sub-order
	if err = data.SetSelectedOrder(parent.TradeId, subOrder.TradeId); err != nil {
		_ = data.UnLockTransactionByTradeId(subTradeID)
		return nil, err
	}
	if err = data.RefreshOrderFamilyExpiration(parent.TradeId, config.GetOrderExpirationTimeDuration()); err != nil {
		_ = data.UnLockTransactionByTradeId(subTradeID)
		return nil, err
	}
	subOrder, err = data.GetOrderInfoByTradeId(subTradeID)
	if err != nil {
		_ = data.UnLockTransactionByTradeId(subTradeID)
		return nil, err
	}

	return buildCheckoutResponse(subOrder)
}
