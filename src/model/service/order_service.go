package service

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/model/response"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/math"
	"github.com/dromara/carbon/v2"
	"github.com/shopspring/decimal"
)

const (
	CnyMinimumPaymentAmount  = 0.01
	UsdtMinimumPaymentAmount = 0.01
	IncrementalMaximumNumber = 100
)

// Lock TTL = order_expiration + (TON only) the confirmation gate's
// max wait, so a payment arriving near order expiry still has the
// lock valid by the time the gate lets it through.
func lockExpirationForNetwork(network string) time.Duration {
	base := config.GetOrderExpirationTimeDuration()
	if strings.ToLower(strings.TrimSpace(network)) == mdb.NetworkTon {
		base += tonLockExpirationBuffer()
	}
	return base
}

var (
	gCreateTransactionLock sync.Mutex
	gOrderProcessingLock   sync.Mutex
)

// apiKeyID safely extracts the primary key from an ApiKey row.
// Returns 0 when apiKey is nil (middleware didn't run — shouldn't happen on authed routes).
func apiKeyID(apiKey *mdb.ApiKey) uint64 {
	if apiKey == nil {
		return 0
	}
	return apiKey.ID
}

func normalizeOrderAddressByNetwork(network, address string) string {
	network = strings.ToLower(strings.TrimSpace(network))
	address = strings.TrimSpace(address)
	switch network {
	case mdb.NetworkEthereum, mdb.NetworkBsc, mdb.NetworkPolygon, mdb.NetworkPlasma:
		return strings.ToLower(address)
	default:
		return address
	}
}

// CreateTransaction creates a new payment order.
func CreateTransaction(req *request.CreateTransactionRequest, apiKey *mdb.ApiKey) (*response.CreateTransactionResponse, error) {
	gCreateTransactionLock.Lock()
	defer gCreateTransactionLock.Unlock()

	token := strings.ToUpper(strings.TrimSpace(req.Token))
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	network := strings.ToLower(strings.TrimSpace(req.Network))
	amountPrecision := data.GetAmountPrecision()
	payAmount := math.MustParsePrecFloat64(req.Amount, amountPrecision)
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

	if !data.IsChainEnabled(network) {
		return nil, constant.ChainNotEnabled
	}
	walletAddress, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		return nil, err
	}
	if len(walletAddress) <= 0 {
		return nil, constant.NotAvailableWalletAddress
	}

	tradeID := GenerateCode()
	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), amountPrecision)
	availableAddress, availableAmount, err := ReserveAvailableWalletAndAmount(tradeID, network, token, amount, walletAddress)
	if err != nil {
		return nil, err
	}
	if availableAddress == "" {
		return nil, constant.NotAvailableAmountErr
	}

	tx := dao.Mdb.Begin()
	order := &mdb.Orders{
		TradeId:        tradeID,
		OrderId:        req.OrderId,
		Amount:         payAmount,
		Currency:       currency,
		ActualAmount:   availableAmount,
		ReceiveAddress: availableAddress,
		Token:          token,
		Network:        network,
		Status:         mdb.StatusWaitPay,
		NotifyUrl:      req.NotifyUrl,
		RedirectUrl:    req.RedirectUrl,
		Name:           req.Name,
		PaymentType:    req.PaymentType,
		PayProvider:    mdb.PaymentProviderOnChain,
		ApiKeyID:       apiKeyID(apiKey),
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
		return fmt.Errorf("load paid order failed, trade_id=%s: %w", req.TradeId, err)
	}

	// Parent order paid directly: expire all sub-orders and release their locks
	if order.ParentTradeId == "" {
		subs, subErr := data.GetActiveSubOrders(order.TradeId)
		if subErr != nil {
			log.Sugar.Errorf("[order] get sub-orders for parent failed, trade_id=%s, err=%v", order.TradeId, subErr)
			return fmt.Errorf("load sub-orders failed, parent_trade_id=%s: %w", order.TradeId, subErr)
		}
		for _, sub := range subs {
			if err = data.ExpireOrderByTradeId(sub.TradeId); err != nil {
				log.Sugar.Warnf("[order] expire sub-order failed, trade_id=%s, err=%v", sub.TradeId, err)
			}
			if sub.PayProvider != "" && sub.PayProvider != mdb.PaymentProviderOnChain {
				if err = data.MarkProviderOrderExpired(sub.TradeId, sub.PayProvider); err != nil {
					log.Sugar.Warnf("[order] expire provider order failed, trade_id=%s, provider=%s, err=%v", sub.TradeId, sub.PayProvider, err)
				}
			}
			if err = data.UnLockTransaction(sub.Network, sub.ReceiveAddress, sub.Token, sub.ActualAmount); err != nil {
				log.Sugar.Warnf("[order] unlock sub-order transaction failed, trade_id=%s, err=%v", sub.TradeId, err)
			}
		}
		return nil
	}

	parent, err := data.GetOrderInfoByTradeId(order.ParentTradeId)
	if err != nil {
		log.Sugar.Errorf("[order] load parent order failed, parent_trade_id=%s, err=%v", order.ParentTradeId, err)
		return fmt.Errorf("load parent order failed, parent_trade_id=%s: %w", order.ParentTradeId, err)
	}

	// Snapshot siblings for lock release after DB state transition commits.
	siblings, err := data.GetSiblingSubOrders(parent.TradeId, order.TradeId)
	if err != nil {
		log.Sugar.Errorf("[order] get sibling sub-orders failed, parent_trade_id=%s, err=%v", parent.TradeId, err)
		return fmt.Errorf("load sibling sub-orders failed, parent_trade_id=%s: %w", parent.TradeId, err)
	}

	finalizeTx := dao.Mdb.Begin()

	// Mark parent as paid with sub-order's payment details
	updatedParent, markErr := data.MarkParentOrderSuccessWithTransaction(finalizeTx, parent.TradeId, order)
	if markErr != nil {
		finalizeTx.Rollback()
		log.Sugar.Errorf("[order] mark parent success failed, parent_trade_id=%s, err=%v", parent.TradeId, markErr)
		return fmt.Errorf("mark parent success failed, parent_trade_id=%s: %w", parent.TradeId, markErr)
	}
	if !updatedParent {
		finalizeTx.Rollback()
		return fmt.Errorf("parent order not updated, trade_id=%s is not in wait-pay status", parent.TradeId)
	}

	if err = data.ExpireSiblingSubOrdersWithTransaction(finalizeTx, parent.TradeId, order.TradeId); err != nil {
		finalizeTx.Rollback()
		return fmt.Errorf("expire sibling sub-orders failed, parent_trade_id=%s: %w", parent.TradeId, err)
	}

	if err = finalizeTx.Commit().Error; err != nil {
		finalizeTx.Rollback()
		return fmt.Errorf("commit parent finalize tx failed, parent_trade_id=%s: %w", parent.TradeId, err)
	}

	// Sub-order should not trigger its own callback (notify_url is empty).
	// OrderSuccessWithTransaction unconditionally sets callback_confirm=No,
	// reset it only after the parent order is successfully finalized.
	if err = data.ResetCallbackConfirmOk(order.TradeId); err != nil {
		log.Sugar.Warnf("[order] reset sub-order callback_confirm failed, trade_id=%s, err=%v", order.TradeId, err)
	}

	// Release parent's own wallet lock
	if err = data.UnLockTransaction(parent.Network, parent.ReceiveAddress, parent.Token, parent.ActualAmount); err != nil {
		log.Sugar.Warnf("[order] unlock parent transaction failed, parent_trade_id=%s, err=%v", parent.TradeId, err)
	}

	// Release sibling locks after their status transitions commit.
	for _, sib := range siblings {
		if sib.PayProvider != "" && sib.PayProvider != mdb.PaymentProviderOnChain {
			if err = data.MarkProviderOrderExpired(sib.TradeId, sib.PayProvider); err != nil {
				log.Sugar.Warnf("[order] expire sibling provider order failed, trade_id=%s, provider=%s, err=%v", sib.TradeId, sib.PayProvider, err)
			}
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
	amountPrecision := data.GetAmountPrecision()
	lockExpiration := lockExpirationForNetwork(network)

	tryLockWalletFunc := func(targetAmount float64) (string, error) {
		for _, address := range walletAddress {
			normalizedAddress := normalizeOrderAddressByNetwork(network, address.Address)
			err := data.LockTransaction(network, normalizedAddress, token, tradeID, targetAmount, lockExpiration)
			if err == nil {
				return normalizedAddress, nil
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
			decimalIncr := decimal.New(1, int32(-amountPrecision))
			availableAmount = math.MustParsePrecFloat64(decimalOldAmount.Add(decimalIncr).InexactFloat64(), amountPrecision)
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

	if network == mdb.PaymentProviderOkPay {
		return switchToOkPay(parent, token)
	}

	// 2. Same token+network as parent → mark selected and return
	if strings.EqualFold(parent.Token, token) && strings.EqualFold(parent.Network, network) {
		_ = data.MarkOrderSelected(parent.TradeId)
		parent.IsSelected = true
		return buildCheckoutResponse(parent), nil
	}

	// 3. Existing active sub-order for this token+network → return it
	existing, err := data.GetSubOrderByTokenNetwork(parent.TradeId, token, network)
	if err != nil {
		return nil, err
	}
	if existing.ID > 0 {
		_ = data.MarkOrderSelected(parent.TradeId)
		_ = data.MarkOrderSelected(existing.TradeId)
		_ = data.RefreshOrderExpiration(parent.TradeId)
		existing.IsSelected = true
		return buildCheckoutResponse(existing), nil
	}

	// 4. Check sub-order limit
	count, err := data.CountActiveSubOrders(parent.TradeId)
	if err != nil {
		return nil, err
	}
	if count >= MaxSubOrders {
		return nil, constant.SubOrderLimitExceeded
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
	if !data.IsChainEnabled(network) {
		return nil, constant.ChainNotEnabled
	}
	walletAddress, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		return nil, err
	}
	if len(walletAddress) <= 0 {
		return nil, constant.NotAvailableWalletAddress
	}

	subTradeID := GenerateCode()
	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), data.GetAmountPrecision())
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
		TradeId:         subTradeID,
		OrderId:         subTradeID, // sub-order uses its own trade_id as order_id (unique constraint)
		ParentTradeId:   parent.TradeId,
		Amount:          parent.Amount,
		Currency:        parent.Currency,
		ActualAmount:    availableAmount,
		ReceiveAddress:  availableAddress,
		Token:           token,
		Network:         network,
		Status:          mdb.StatusWaitPay,
		IsSelected:      true,
		NotifyUrl:       "",
		RedirectUrl:     parent.RedirectUrl,
		Name:            parent.Name,
		CallBackConfirm: mdb.CallBackConfirmOk, // don't trigger callback on sub-order
		PaymentType:     parent.PaymentType,
		PayProvider:     mdb.PaymentProviderOnChain,
		ApiKeyID:        parent.ApiKeyID, // inherit from parent so resolveOrderApiKey never fails
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
	_ = data.MarkOrderSelected(parent.TradeId)
	_ = data.RefreshOrderExpiration(parent.TradeId)

	return buildCheckoutResponse(subOrder), nil
}

func buildCheckoutResponse(order *mdb.Orders) *response.CheckoutCounterResponse {
	return &response.CheckoutCounterResponse{
		TradeId:        order.TradeId,
		Amount:         order.Amount,
		ActualAmount:   order.ActualAmount,
		Token:          order.Token,
		Currency:       order.Currency,
		ReceiveAddress: order.ReceiveAddress,
		Network:        order.Network,
		ExpirationTime: order.CreatedAt.AddMinutes(config.GetOrderExpirationTime()).TimestampMilli(),
		RedirectUrl:    order.RedirectUrl,
		PaymentUrl:     fmt.Sprintf("%s/pay/checkout-counter/%s", config.GetAppUri(), order.TradeId),
		CreatedAt:      order.CreatedAt.TimestampMilli(),
		IsSelected:     order.IsSelected,
	}
}

func switchToOkPay(parent *mdb.Orders, token string) (*response.CheckoutCounterResponse, error) {
	if !data.GetOkPayEnabled() {
		return nil, constant.PaymentProviderNotEnabled
	}
	if data.GetOkPayShopID() == "" || data.GetOkPayShopToken() == "" || data.GetOkPayAPIURL() == "" || data.GetOkPayCallbackURL() == "" {
		return nil, constant.PaymentProviderConfigErr
	}
	if !okPayTokenAllowed(token) {
		return nil, constant.PaymentProviderNotSupport
	}

	existing, err := data.GetSubOrderByTokenPayProvider(parent.TradeId, token, mdb.PaymentProviderOkPay)
	if err != nil {
		return nil, err
	}
	if existing.ID > 0 {
		providerRow, err := data.GetProviderOrderByTradeIDAndProvider(existing.TradeId, mdb.PaymentProviderOkPay)
		if err != nil {
			return nil, err
		}
		if providerRow.ID == 0 || strings.TrimSpace(providerRow.PayURL) == "" {
			return nil, constant.SystemErr
		}
		_ = data.MarkOrderSelected(parent.TradeId)
		_ = data.MarkOrderSelected(existing.TradeId)
		_ = data.RefreshOrderExpiration(parent.TradeId)
		existing.IsSelected = true
		resp := buildCheckoutResponse(existing)
		resp.PaymentUrl = providerRow.PayURL
		return resp, nil
	}

	count, err := data.CountActiveSubOrders(parent.TradeId)
	if err != nil {
		return nil, err
	}
	if count >= MaxSubOrders {
		return nil, constant.SubOrderLimitExceeded
	}

	rate := config.GetRateForCoin(strings.ToLower(token), strings.ToLower(parent.Currency))
	if rate <= 0 {
		return nil, constant.RateAmountErr
	}
	decimalPayAmount := decimal.NewFromFloat(parent.Amount)
	decimalTokenAmount := decimalPayAmount.Mul(decimal.NewFromFloat(rate))
	if decimalTokenAmount.Cmp(decimal.NewFromFloat(UsdtMinimumPaymentAmount)) == -1 {
		return nil, constant.PayAmountErr
	}

	subTradeID := GenerateCode()
	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), data.GetAmountPrecision())
	returnURL := strings.TrimSpace(parent.RedirectUrl)
	if returnURL == "" {
		returnURL = data.GetOkPayReturnURL()
	}
	if returnURL == "" {
		returnURL = fmt.Sprintf("%s/pay/checkout-counter/%s", config.GetAppUri(), parent.TradeId)
	}

	tx := dao.Mdb.Begin()
	subOrder := &mdb.Orders{
		TradeId:         subTradeID,
		OrderId:         subTradeID,
		ParentTradeId:   parent.TradeId,
		Amount:          parent.Amount,
		Currency:        parent.Currency,
		ActualAmount:    amount,
		ReceiveAddress:  "OKPAY",
		Token:           token,
		Network:         mdb.NetworkTron,
		Status:          mdb.StatusWaitPay,
		IsSelected:      true,
		NotifyUrl:       "",
		RedirectUrl:     parent.RedirectUrl,
		Name:            parent.Name,
		CallBackConfirm: mdb.CallBackConfirmOk,
		PaymentType:     parent.PaymentType,
		PayProvider:     mdb.PaymentProviderOkPay,
		ApiKeyID:        parent.ApiKeyID,
	}
	if err = data.CreateOrderWithTransaction(tx, subOrder); err != nil {
		tx.Rollback()
		return nil, err
	}
	providerRow := &mdb.ProviderOrder{
		TradeId:         subTradeID,
		Provider:        mdb.PaymentProviderOkPay,
		ProviderOrderID: "",
		PayURL:          "",
		Amount:          amount,
		Coin:            token,
		Status:          mdb.ProviderOrderStatusCreating,
	}
	if err = data.CreateProviderOrderWithTransaction(tx, providerRow); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err = tx.Commit().Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	okpayOrder, err := createOkPayDepositOrder(subTradeID, amount, token, returnURL)
	if err != nil {
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderOkPay)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, err
	}
	if err = data.UpdateProviderOrderCreated(subTradeID, mdb.PaymentProviderOkPay, okpayOrder.ProviderOrderID, okpayOrder.PayURL); err != nil {
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderOkPay)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, err
	}

	_ = data.MarkOrderSelected(parent.TradeId)
	_ = data.RefreshOrderExpiration(parent.TradeId)

	resp := buildCheckoutResponse(subOrder)
	resp.PaymentUrl = okpayOrder.PayURL
	return resp, nil
}

func okPayTokenAllowed(token string) bool {
	for _, item := range data.GetOkPayAllowTokens() {
		if strings.EqualFold(item, token) {
			return true
		}
	}
	return false
}
