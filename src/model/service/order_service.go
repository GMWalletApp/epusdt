package service

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"strconv"
	"strings"
	"sync"

	scanner "github.com/GMWalletApp/ChainScanner"
	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/model/response"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/math"
	"github.com/GMWalletApp/epusdt/util/security"
	"github.com/dromara/carbon/v2"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CnyMinimumPaymentAmount  = 0.01
	UsdtMinimumPaymentAmount = 0.01
	IncrementalMaximumNumber = 100
)

var (
	gCreateTransactionLock sync.Mutex
	gOrderProcessingLock   sync.Mutex
)

var (
	manualPaymentWaitPayStatuses = []int{mdb.StatusWaitPay}
	adminManualPaymentStatuses   = []int{mdb.StatusWaitPay, mdb.StatusExpired, mdb.StatusManualReview}
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
	if scanner.IsEVM(network) {
		return strings.ToLower(address)
	}
	switch network {
	case mdb.NetworkTon:
		if normalized, err := addressutil.NormalizeTonAddress(address); err == nil {
			return normalized
		}
		return address
	case mdb.NetworkAptos, mdb.NetworkSui:
		if normalized, err := addressutil.NormalizeMoveAddress(address); err == nil {
			return normalized
		}
		return address
	default:
		return address
	}
}

func ensureEnabledOrderAsset(network, token string) (*mdb.ChainToken, error) {
	tokenRow, err := data.GetEnabledChainTokenBySymbol(network, token)
	if err != nil {
		return nil, err
	}
	if tokenRow == nil || tokenRow.ID == 0 {
		return nil, constant.SupportedAssetNotFound
	}
	if !ChainTokenReadyForPayment(*tokenRow) {
		return nil, constant.SupportedAssetNotFound
	}
	return tokenRow, nil
}

func validateCreateTransactionTokenNetwork(token, network string) error {
	if (token == "") != (network == "") {
		return constant.ParamsMarshalErr
	}
	return nil
}

func buildCreateTransactionResponse(order *mdb.Orders) *response.CreateTransactionResponse {
	expirationTime := order.CreateResponseExpiresAt
	if expirationTime == 0 {
		expirationTime = order.CreatedAt.AddMinutes(config.GetOrderExpirationTime()).Timestamp()
	}
	return &response.CreateTransactionResponse{
		TradeId:        order.TradeId,
		OrderId:        order.OrderId,
		Amount:         order.Amount,
		Currency:       order.Currency,
		ActualAmount:   order.ActualAmount,
		ReceiveAddress: order.ReceiveAddress,
		Token:          order.Token,
		Status:         order.Status,
		ExpirationTime: expirationTime,
		PaymentUrl:     fmt.Sprintf("%s/pay/checkout-counter/%s", config.GetAppUri(), order.TradeId),
	}
}

// Hash the original merchant inputs before checkout can change the payment target.
// Keep the field order stable so stored hashes remain comparable after upgrades.
func gmpayCreateRequestHash(order *mdb.Orders, requestedAmount float64) string {
	if order.PaymentType != mdb.PaymentTypeGmpay || order.ApiKeyID == 0 {
		return ""
	}
	fields := []string{
		order.OrderId, order.Currency, strconv.FormatFloat(requestedAmount, 'g', -1, 64),
		order.Token, order.Network, order.NotifyUrl, order.RedirectUrl, order.Name,
	}
	if order.CallbackVersion == 2 {
		fields = append(fields, "callback_version=2")
	}
	payload, _ := json.Marshal(fields) // An array of strings always encodes.
	return fmt.Sprintf("%x", sha256.Sum256(payload))
}

func replayCreateTransaction(existing, requested *mdb.Orders) (*response.CreateTransactionResponse, error) {
	if requested.CreateRequestHash == "" || existing.CreateRequestHash != requested.CreateRequestHash ||
		existing.ApiKeyID != requested.ApiKeyID || existing.PaymentType != requested.PaymentType || existing.ParentTradeId != "" {
		return nil, constant.OrderAlreadyExists
	}
	if requested.PaymentType == mdb.PaymentTypeEpay {
		if existing.Status != mdb.StatusWaitSelect && existing.Status != mdb.StatusWaitPay {
			return nil, constant.OrderAlreadyExists
		}
		if existing.CreatedAt.AddMinutes(config.GetOrderExpirationTime()).TimestampMilli() <= carbon.Now().TimestampMilli() {
			return nil, constant.OrderAlreadyExists
		}
	}
	return buildCreateTransactionResponse(existing), nil
}

// ReopenEPayCashier resolves a signed retry before applying today's checkout defaults.
func ReopenEPayCashier(orderID string, apiKeyID uint64, submitHash string) (string, error) {
	existing, err := data.GetOrderInfoByOrderId(orderID)
	if err != nil || existing.ID == 0 {
		return "", err
	}
	req := &mdb.Orders{PaymentType: mdb.PaymentTypeEpay, ApiKeyID: apiKeyID, CreateRequestHash: submitHash}
	replay, err := replayCreateTransaction(existing, req)
	if err != nil {
		return "", err
	}
	return replay.TradeId, nil
}

// Capture the deadline at the same instant as creation. The cashier may later
// renew its own payment window by updating created_at.
func insertMerchantOrder(tx *gorm.DB, order *mdb.Orders, key *mdb.ApiKey) error {
	if order.ApiKeyID != 0 {
		id, err := dao.PinCallbackSigningKey(tx, key)
		if err != nil {
			return err
		}
		order.CallbackSigningKeyID = id
	}
	if order.CreateRequestHash != "" {
		now := carbon.Now()
		order.CreatedAt = *carbon.NewTime(now)
		order.CreateResponseExpiresAt = now.AddMinutes(config.GetOrderExpirationTime()).Timestamp()
	}
	return data.CreateOrderWithTransaction(tx, order)
}

// A concurrent insert on another gateway may have claimed the unique merchant order ID.
func replayAfterCreateError(requested *mdb.Orders, createErr error) (*response.CreateTransactionResponse, error) {
	existing, err := data.GetOrderInfoByOrderId(requested.OrderId)
	if err != nil || existing.ID == 0 {
		return nil, createErr
	}
	return replayCreateTransaction(existing, requested)
}

// CreateTransaction creates a new payment order.
func CreateTransaction(req *request.CreateTransactionRequest, apiKey *mdb.ApiKey) (*response.CreateTransactionResponse, error) {
	token := strings.ToUpper(strings.TrimSpace(req.Token))
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	network := strings.ToLower(strings.TrimSpace(req.Network))
	if err := validateCreateTransactionTokenNetwork(token, network); err != nil {
		return nil, err
	}
	notifyURL := strings.TrimSpace(req.NotifyUrl)
	redirectURL, name := req.RedirectUrl, req.Name
	paymentType := mdb.PaymentTypeGmpay
	if strings.EqualFold(req.PaymentType, mdb.PaymentTypeEpay) {
		paymentType = mdb.PaymentTypeEpay
	}
	callbackVersion := req.CallbackVersion
	if callbackVersion == 0 {
		callbackVersion = 1
	}
	if callbackVersion != 1 && (callbackVersion != 2 || paymentType != mdb.PaymentTypeGmpay) {
		return nil, constant.ParamsMarshalErr
	}
	epayType := ""
	if paymentType == mdb.PaymentTypeEpay {
		epayType = strings.TrimSpace(req.EpayType)
	} else {
		redirectURL, name = strings.TrimSpace(redirectURL), strings.TrimSpace(name)
	}

	resolvedNotifyURL := notifyURL
	if resolvedNotifyURL == "" && apiKey != nil {
		resolvedNotifyURL = strings.TrimSpace(apiKey.NotifyUrl)
	}
	notifyErr := security.ValidatePublicHTTPURL(resolvedNotifyURL)
	if notifyErr != nil && (notifyURL != "" || apiKey == nil) {
		return nil, constant.NotifyURLErr
	}

	gCreateTransactionLock.Lock()
	defer gCreateTransactionLock.Unlock()

	amountPrecision := data.GetAmountPrecision()
	payAmount := math.MustParsePrecFloat64(req.Amount, amountPrecision)
	order := &mdb.Orders{
		OrderId:         req.OrderId,
		Amount:          payAmount,
		Currency:        currency,
		Token:           token,
		Network:         network,
		NotifyUrl:       notifyURL,
		RedirectUrl:     redirectURL,
		Name:            name,
		EpayType:        epayType,
		PaymentType:     paymentType,
		CallbackVersion: callbackVersion,
		PayProvider:     mdb.PaymentProviderOnChain,
		ApiKeyID:        apiKeyID(apiKey),
	}
	order.CreateRequestHash = gmpayCreateRequestHash(order, req.Amount)
	if paymentType == mdb.PaymentTypeEpay && apiKeyID(apiKey) != 0 {
		order.CreateRequestHash = req.EPaySubmitHash
	}
	exist, err := data.GetOrderInfoByOrderId(req.OrderId)
	if err != nil {
		return nil, err
	}
	if exist.ID > 0 {
		return replayCreateTransaction(exist, order)
	}

	// The merchant's current default cannot alter an existing signed request.
	if notifyErr != nil {
		return nil, constant.NotifyURLErr
	}
	order.NotifyUrl = resolvedNotifyURL

	decimalPayAmount := decimal.NewFromFloat(payAmount)
	if decimalPayAmount.Cmp(decimal.NewFromFloat(CnyMinimumPaymentAmount)) == -1 {
		return nil, constant.PayAmountErr
	}

	if token == "" && network == "" {
		order.TradeId = GenerateCode()
		order.Status = mdb.StatusWaitSelect
		if err = dao.Mdb.Transaction(func(tx *gorm.DB) error { return insertMerchantOrder(tx, order, apiKey) }); err != nil {
			return replayAfterCreateError(order, err)
		}
		return buildCreateTransactionResponse(order), nil
	}

	// The cashier creates hosted OKX orders through /pay/switch-network.
	if network == mdb.PaymentProviderXLayer {
		return nil, constant.ChainNotEnabled
	}
	if !data.IsChainEnabled(network) {
		return nil, constant.ChainNotEnabled
	}
	if _, err = ensureEnabledOrderAsset(network, token); err != nil {
		return nil, err
	}

	decimalTokenAmount, err := OnChainTokenAmount(payAmount, currency, token)
	if err != nil {
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
	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), amountPrecision)
	wallet, availableAmount, err := reserveAvailableWalletAndAmount(tradeID, network, token, amount, walletAddress)
	if err != nil {
		return nil, err
	}
	if wallet.ID == 0 {
		return nil, constant.NotAvailableAmountErr
	}

	order.TradeId = tradeID
	order.ActualAmount = availableAmount
	order.ReceiveAddress = wallet.Address
	order.Status = mdb.StatusWaitPay
	if err = data.WithReceivingWallet(wallet.ID, func(tx *gorm.DB) error {
		return insertMerchantOrder(tx, order, apiKey)
	}); err != nil {
		_ = data.UnLockTransactionByTradeId(tradeID)
		return replayAfterCreateError(order, err)
	}

	return buildCreateTransactionResponse(order), nil
}

// OrderProcessing marks an order as paid and releases its sqlite reservation.
func OrderProcessing(req *request.OrderProcessingRequest) error {
	return orderProcessingWithAllowedStatuses(req, manualPaymentWaitPayStatuses)
}

func orderProcessingWithAllowedStatuses(req *request.OrderProcessingRequest, allowed []int) error {
	gOrderProcessingLock.Lock()
	defer gOrderProcessingLock.Unlock()
	var release []mdb.Orders
	err := dao.Mdb.Transaction(func(tx *gorm.DB) error {
		var order mdb.Orders
		if err := tx.Where("trade_id = ?", req.TradeId).Take(&order).Error; err != nil {
			return err
		}
		parent := order.TradeId
		if order.ParentTradeId != "" {
			parent = order.ParentTradeId
		}
		var head mdb.Orders
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_id = ?", parent).Take(&head).Error; err != nil {
			return err
		}

		existing, err := data.GetOrderByBlockIdWithTransaction(tx, req.BlockTransactionId)
		if err != nil {
			return err
		}
		if existing.ID != 0 {
			return constant.OrderBlockAlreadyProcess
		}
		if !manualPaymentStatusAllowed(head.Status, allowed) {
			return constant.OrderStatusConflict
		}
		ok, err := data.OrderSuccessWithStatusesWithTransaction(tx, req, allowed)
		if err != nil {
			return err
		}
		if !ok {
			return constant.OrderStatusConflict
		}
		if order.ParentTradeId != "" {
			ok, err = data.MarkParentOrderSuccessWithStatusesWithTransaction(tx, parent, &order, allowed)
			if err != nil {
				return err
			}
			if !ok {
				return constant.OrderStatusConflict
			}
			if err = tx.Model(&order).Update("callback_confirm", mdb.CallBackConfirmOk).Error; err != nil {
				return err
			}
		}
		siblings := tx.Model(&mdb.Orders{}).Where("parent_trade_id = ? AND trade_id <> ? AND status IN ?", parent, order.TradeId, []int{mdb.StatusWaitPay, mdb.StatusConfirming})
		var others []mdb.Orders
		if err = siblings.Find(&others).Error; err != nil {
			return err
		}
		for _, sub := range others {
			if err = tx.Model(&sub).Where("status IN ?", []int{mdb.StatusWaitPay, mdb.StatusConfirming}).Update("status", mdb.StatusExpired).Error; err != nil {
				log.Sugar.Warnf("[order] could not expire sibling %s: %v", sub.TradeId, err)
				continue
			}
			if !isOnChainOrder(sub.PayProvider) {
				if err = tx.Model(&mdb.ProviderOrder{}).Where("trade_id = ? AND provider = ?", sub.TradeId, sub.PayProvider).Where("status IN ?", []string{mdb.ProviderOrderStatusPending, mdb.ProviderOrderStatusCreating}).Update("status", mdb.ProviderOrderStatusExpired).Error; err != nil {
					return err
				}
			}
			release = append(release, sub)
		}
		release = append(release, order)
		if head.ID != order.ID {
			release = append(release, head)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, o := range release {
		if hasTransactionLock(&o) {
			if err := data.UnLockTransactionByTradeId(o.TradeId); err != nil {
				log.Sugar.Warnf("[order] release order %s: %v", o.TradeId, err)
			}
		}
	}
	return nil
}

// EnsurePaymentWatch is wired during bootstrap before the HTTP server starts.
var EnsurePaymentWatch func(network, address string) error

func shuffleWalletAddressesRandom(wallets []mdb.WalletAddress) {
	mathrand.Shuffle(len(wallets), func(i, j int) {
		wallets[i], wallets[j] = wallets[j], wallets[i]
	})
}

// shuffleWalletAddresses randomizes lock-try order. Tests may replace it.
var shuffleWalletAddresses = shuffleWalletAddressesRandom

// ReserveAvailableWalletAndAmount finds and locks a network+address+token+amount pair.
func ReserveAvailableWalletAndAmount(tradeID string, network string, token string, amount float64, walletAddress []mdb.WalletAddress) (string, float64, error) {
	wallet, reservedAmount, err := reserveAvailableWalletAndAmount(tradeID, network, token, amount, walletAddress)
	return wallet.Address, reservedAmount, err
}

func reserveAvailableWalletAndAmount(tradeID string, network string, token string, amount float64, walletAddress []mdb.WalletAddress) (mdb.WalletAddress, float64, error) {
	availableAmount := amount
	amountPrecision := data.GetAmountPrecision()
	shuffleWalletAddresses(walletAddress)

	tryLockWalletFunc := func(targetAmount float64) (mdb.WalletAddress, error) {
		for _, address := range walletAddress {
			normalizedAddress := normalizeOrderAddressByNetwork(network, address.Address)
			if EnsurePaymentWatch != nil {
				if err := EnsurePaymentWatch(network, normalizedAddress); err != nil {
					return mdb.WalletAddress{}, err
				}
			}
			err := data.LockTransaction(network, normalizedAddress, token, tradeID, targetAmount, config.GetOrderExpirationTimeDuration())
			if err == nil {
				address.Address = normalizedAddress
				return address, nil
			}
			if errors.Is(err, data.ErrTransactionLocked) {
				continue
			}
			return mdb.WalletAddress{}, err
		}
		return mdb.WalletAddress{}, nil
	}

	for i := 0; i < IncrementalMaximumNumber; i++ {
		wallet, err := tryLockWalletFunc(availableAmount)
		if err != nil {
			return mdb.WalletAddress{}, 0, err
		}
		if wallet.Address != "" {
			return wallet, availableAmount, nil
		}
		decimalOldAmount := decimal.NewFromFloat(availableAmount)
		decimalIncr := decimal.New(1, int32(-amountPrecision))
		availableAmount = math.MustParsePrecFloat64(decimalOldAmount.Add(decimalIncr).InexactFloat64(), amountPrecision)
	}
	return mdb.WalletAddress{}, availableAmount, nil
}

// GenerateCode creates a unique trade id.
func GenerateCode() string {
	buf := make([]byte, 18)
	if _, err := cryptorand.Read(buf); err != nil {
		panic(fmt.Sprintf("generate trade id: crypto/rand failed: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(buf)
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

// SubmitManualPayment verifies a submitted transaction hash and marks the
// matching admin-selected on-chain order paid. Admin mark-paid may repair
// waiting or expired orders after the submitted chain transaction is verified.
func SubmitManualPayment(tradeId, blockTransactionId string) (*response.ManualPaymentResponse, error) {
	tradeId = strings.TrimSpace(tradeId)
	blockTransactionId = strings.TrimSpace(blockTransactionId)

	order, err := GetOrderInfoByTradeId(tradeId)
	if err != nil {
		return nil, err
	}
	if order.ParentTradeId == "" {
		subs, subErr := data.GetAllSubOrders(order.TradeId)
		if subErr != nil {
			return nil, subErr
		}
		for i := range subs {
			sub := &subs[i]
			if !manualPaymentStatusAllowed(sub.Status, adminManualPaymentStatuses) || !isOnChainOrder(sub.PayProvider) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(sub.ReviewTransactionHash), blockTransactionId) || sub.Status == mdb.StatusManualReview {
				if resp, err := submitManualPaymentForOrder(sub, blockTransactionId, adminManualPaymentStatuses); err == nil {
					return resp, nil
				}
			}
		}
		resp, err := submitManualPaymentForOrder(order, blockTransactionId, adminManualPaymentStatuses)
		if err == nil {
			return resp, nil
		}
		for i := range subs {
			sub := &subs[i]
			if !manualPaymentStatusAllowed(sub.Status, adminManualPaymentStatuses) || !isOnChainOrder(sub.PayProvider) {
				continue
			}
			if subResp, subPayErr := submitManualPaymentForOrder(sub, blockTransactionId, adminManualPaymentStatuses); subPayErr == nil {
				return subResp, nil
			}
		}
		return nil, err
	}
	return submitManualPaymentForOrder(order, blockTransactionId, adminManualPaymentStatuses)
}

// SubmitCashierManualPayment is the public cashier variant. It rejects hashes
// already stored on any order before touching RPC so repeated/public probes do
// not spend RPC quota. Admin mark-paid intentionally keeps using
// SubmitManualPayment and the existing verification path.
func SubmitCashierManualPayment(tradeId, blockTransactionId string) (*response.ManualPaymentResponse, error) {
	tradeId = strings.TrimSpace(tradeId)
	blockTransactionId = strings.TrimSpace(blockTransactionId)

	order, err := GetOrderInfoByTradeId(tradeId)
	if err != nil {
		return nil, err
	}
	if err = validateManualPaymentOrderWithStatuses(order, manualPaymentWaitPayStatuses); err != nil {
		return nil, err
	}
	if err = ensureManualBlockTransactionUnused(order, blockTransactionId); err != nil {
		return nil, err
	}
	return submitManualPaymentForOrder(order, blockTransactionId, manualPaymentWaitPayStatuses)
}

func submitManualPaymentForOrder(order *mdb.Orders, blockTransactionId string, allowedStatuses []int) (*response.ManualPaymentResponse, error) {
	blockTransactionId = strings.TrimSpace(blockTransactionId)
	if err := validateManualPaymentOrderWithStatuses(order, allowedStatuses); err != nil {
		return nil, err
	}

	verifiedBlockTransactionID, err := ValidateManualOrderPayment(order, blockTransactionId)
	if err != nil {
		var rspErr *constant.RspError
		if errors.As(err, &rspErr) {
			return nil, err
		}
		return nil, constant.ManualPaymentVerifyErr
	}
	if err = orderProcessingWithAllowedStatuses(&request.OrderProcessingRequest{
		ReceiveAddress:     order.ReceiveAddress,
		Currency:           order.Currency,
		Token:              order.Token,
		Network:            order.Network,
		Amount:             order.ActualAmount,
		TradeId:            order.TradeId,
		BlockTransactionId: verifiedBlockTransactionID,
	}, allowedStatuses); err != nil {
		return nil, err
	}

	updatedOrder, err := GetOrderInfoByTradeId(order.TradeId)
	if err != nil {
		return nil, err
	}
	return &response.ManualPaymentResponse{
		TradeId:            updatedOrder.TradeId,
		Status:             updatedOrder.Status,
		BlockTransactionId: updatedOrder.BlockTransactionId,
	}, nil
}

func validateManualPaymentOrder(order *mdb.Orders) error {
	return validateManualPaymentOrderWithStatuses(order, manualPaymentWaitPayStatuses)
}

func validateManualPaymentOrderWithStatuses(order *mdb.Orders, allowedStatuses []int) error {
	if !manualPaymentStatusAllowed(order.Status, allowedStatuses) {
		return constant.OrderNotWaitPay
	}
	if !isOnChainOrder(order.PayProvider) {
		return constant.ManualPaymentProviderErr
	}
	return nil
}

func manualPaymentStatusAllowed(status int, allowedStatuses []int) bool {
	for _, allowed := range allowedStatuses {
		if status == allowed {
			return true
		}
	}
	return false
}

func isOnChainOrder(payProvider string) bool {
	payProvider = strings.TrimSpace(payProvider)
	return payProvider == "" || payProvider == mdb.PaymentProviderOnChain
}

func hasTransactionLock(order *mdb.Orders) bool {
	return order != nil &&
		isOnChainOrder(order.PayProvider) &&
		strings.TrimSpace(order.Network) != "" &&
		strings.TrimSpace(order.ReceiveAddress) != "" &&
		strings.TrimSpace(order.Token) != "" &&
		order.ActualAmount > 0
}

const MaxSubOrders = 1

// SwitchNetwork completes a status=4 placeholder in place, or creates/returns
// an existing sub-order for a different payment target once the parent is
// already concrete.
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
	if parent.Status != mdb.StatusWaitPay && parent.Status != mdb.StatusWaitSelect {
		return nil, constant.OrderNotWaitPay
	}

	if network == mdb.PaymentProviderOkPay {
		return switchToOkPay(parent, token)
	}

	if network == mdb.PaymentProviderXLayer {
		return switchToXLayer(parent, token)
	}

	if parent.Status == mdb.StatusWaitSelect {
		return completeWaitSelectOrder(parent, token, network)
	}

	// 2. Same token+network as parent → mark selected and return
	if strings.EqualFold(parent.Token, token) && strings.EqualFold(parent.Network, network) {
		if err := data.SelectCheckoutOrder(dao.Mdb, parent.TradeId, parent, false); err != nil {
			return nil, err
		}
		return buildCheckoutResponse(parent), nil
	}

	// 3. Existing active sub-order for this token+network → return it
	existing, err := data.GetSubOrderByTokenNetwork(parent.TradeId, token, network)
	if err != nil {
		return nil, err
	}
	if existing.ID > 0 {
		if err := data.SelectCheckoutOrder(dao.Mdb, parent.TradeId, existing, true); err != nil {
			return nil, err
		}
		return buildCheckoutResponse(existing), nil
	}

	// 4. Only one sub-order may ever be created under a parent. Existing
	// active sub-orders are reused above; any other target after that is blocked.
	count, err := data.CountSubOrders(parent.TradeId)
	if err != nil {
		return nil, err
	}
	if count >= MaxSubOrders {
		return nil, constant.SubOrderLimitExceeded
	}

	// 5. Validate the target chain and asset before any rate lookup so
	// disabled/unknown tokens return the stable unsupported-asset error.
	if !data.IsChainEnabled(network) {
		return nil, constant.ChainNotEnabled
	}
	if _, err = ensureEnabledOrderAsset(network, token); err != nil {
		return nil, err
	}

	// 6. Calculate amount for the new network
	decimalTokenAmount, err := OnChainTokenAmount(parent.Amount, parent.Currency, token)
	if err != nil {
		return nil, err
	}

	// 7. Find and lock wallet
	walletAddress, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		return nil, err
	}
	if len(walletAddress) <= 0 {
		return nil, constant.NotAvailableWalletAddress
	}

	subTradeID := GenerateCode()
	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), data.GetAmountPrecision())
	wallet, availableAmount, err := reserveAvailableWalletAndAmount(subTradeID, network, token, amount, walletAddress)
	if err != nil {
		return nil, err
	}
	if wallet.ID == 0 {
		return nil, constant.NotAvailableAmountErr
	}

	// 8. Create sub-order
	subOrder := &mdb.Orders{
		TradeId:         subTradeID,
		OrderId:         subTradeID, // sub-order uses its own trade_id as order_id (unique constraint)
		ParentTradeId:   parent.TradeId,
		Amount:          parent.Amount,
		Currency:        parent.Currency,
		ActualAmount:    availableAmount,
		ReceiveAddress:  wallet.Address,
		Token:           token,
		Network:         network,
		Status:          mdb.StatusWaitPay,
		NotifyUrl:       "",
		RedirectUrl:     parent.RedirectUrl,
		Name:            parent.Name,
		EpayType:        parent.EpayType,
		CallBackConfirm: mdb.CallBackConfirmOk, // don't trigger callback on sub-order
		PaymentType:     parent.PaymentType,
		PayProvider:     mdb.PaymentProviderOnChain,
		ApiKeyID:        parent.ApiKeyID,

		CallbackSigningKeyID: parent.CallbackSigningKeyID,
	}
	if err = data.WithReceivingWallet(wallet.ID, func(tx *gorm.DB) error {
		if err := data.CreateOrderWithTransaction(tx, subOrder); err != nil {
			return err
		}
		return data.SelectCheckoutOrder(tx, parent.TradeId, subOrder, true)
	}); err != nil {
		_ = data.UnLockTransactionByTradeId(subTradeID)
		return nil, err
	}

	return buildCheckoutResponse(subOrder), nil
}

func completeWaitSelectOrder(parent *mdb.Orders, token string, network string) (*response.CheckoutCounterResponse, error) {
	if !data.IsChainEnabled(network) {
		return nil, constant.ChainNotEnabled
	}
	if _, err := ensureEnabledOrderAsset(network, token); err != nil {
		return nil, err
	}

	decimalTokenAmount, err := OnChainTokenAmount(parent.Amount, parent.Currency, token)
	if err != nil {
		return nil, err
	}

	walletAddress, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		return nil, err
	}
	if len(walletAddress) <= 0 {
		return nil, constant.NotAvailableWalletAddress
	}

	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), data.GetAmountPrecision())
	wallet, availableAmount, err := reserveAvailableWalletAndAmount(parent.TradeId, network, token, amount, walletAddress)
	if err != nil {
		return nil, err
	}
	if wallet.ID == 0 {
		return nil, constant.NotAvailableAmountErr
	}

	err = data.WithReceivingWallet(wallet.ID, func(tx *gorm.DB) error {
		updated, err := data.CompleteWaitSelectOrderWithTransaction(tx, parent.TradeId, network, token, wallet.Address, availableAmount)
		if err == nil && !updated {
			return constant.OrderStatusConflict
		}
		return err
	})
	if err != nil {
		_ = data.UnLockTransactionByTradeId(parent.TradeId)
		return nil, err
	}

	order, err := data.GetOrderInfoByTradeId(parent.TradeId)
	if err != nil {
		return nil, err
	}
	return buildCheckoutResponse(order), nil
}

func buildCheckoutResponse(order *mdb.Orders) *response.CheckoutCounterResponse {
	paymentType := mdb.PaymentTypeGmpay
	if isEPayOrder(order) {
		paymentType = mdb.PaymentTypeEpay
	}
	return &response.CheckoutCounterResponse{
		TradeId:         order.TradeId,
		Amount:          order.Amount,
		ActualAmount:    order.ActualAmount,
		Token:           order.Token,
		Currency:        order.Currency,
		ReceiveAddress:  order.ReceiveAddress,
		Network:         order.Network,
		Status:          order.Status,
		PaymentType:     strings.ToLower(paymentType),
		ExpirationTime:  order.CreatedAt.AddMinutes(config.GetOrderExpirationTime()).TimestampMilli(),
		RedirectUrl:     buildPublicRedirectURL(order),
		MerchantHomeURL: epayMerchantHome(order),
		CreatedAt:       order.CreatedAt.TimestampMilli(),
		ServerTime:      carbon.Now().TimestampMilli(),
		IsSelected:      order.IsSelected,
	}
}

func completeWaitSelectOkPayOrder(parent *mdb.Orders, token string) (*response.CheckoutCounterResponse, error) {
	rate := config.GetRateForCoin(strings.ToLower(token), strings.ToLower(parent.Currency))
	if rate <= 0 {
		return nil, constant.RateAmountErr
	}
	decimalPayAmount := decimal.NewFromFloat(parent.Amount)
	decimalTokenAmount := decimalPayAmount.Mul(decimal.NewFromFloat(rate))
	if decimalTokenAmount.Cmp(decimal.NewFromFloat(UsdtMinimumPaymentAmount)) == -1 {
		return nil, constant.PayAmountErr
	}

	amount := math.MustParsePrecFloat64(decimalTokenAmount.InexactFloat64(), data.GetAmountPrecision())
	returnURL := strings.TrimSpace(parent.RedirectUrl)
	if returnURL == "" {
		returnURL = data.GetOkPayReturnURL()
	}
	if returnURL == "" {
		returnURL = fmt.Sprintf("%s/pay/checkout-counter/%s", config.GetAppUri(), parent.TradeId)
	}

	tx := dao.Mdb.Begin()
	providerRow := &mdb.ProviderOrder{
		TradeId:         parent.TradeId,
		Provider:        mdb.PaymentProviderOkPay,
		ProviderOrderID: "",
		PayURL:          "",
		Amount:          amount,
		Coin:            token,
		Status:          mdb.ProviderOrderStatusCreating,
		NotifyRaw:       "",
	}
	if err := data.UpsertProviderOrderCreatingWithTransaction(tx, providerRow); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	okpayOrder, err := createOkPayDepositOrder(parent.TradeId, amount, token, returnURL)
	if err != nil {
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderOkPay)
		return nil, constant.PaymentProviderCreateErr
	}

	finalizeTx := dao.Mdb.Begin()
	if err = data.UpdateProviderOrderCreatedWithTransaction(finalizeTx, parent.TradeId, mdb.PaymentProviderOkPay, okpayOrder.ProviderOrderID, okpayOrder.PayURL); err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderOkPay)
		return nil, err
	}
	updated, err := data.CompleteWaitSelectOkPayOrderWithTransaction(finalizeTx, parent.TradeId, token, amount)
	if err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderOkPay)
		return nil, err
	}
	if !updated {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderOkPay)
		return nil, constant.OrderStatusConflict
	}
	if err = finalizeTx.Commit().Error; err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderOkPay)
		return nil, err
	}

	order, err := data.GetOrderInfoByTradeId(parent.TradeId)
	if err != nil {
		return nil, err
	}
	resp := buildCheckoutResponse(order)
	resp.PaymentUrl = okpayOrder.PayURL
	return resp, nil
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

	if parent.Status == mdb.StatusWaitSelect {
		return completeWaitSelectOkPayOrder(parent, token)
	}

	if parent.PayProvider == mdb.PaymentProviderOkPay && strings.EqualFold(parent.Token, token) {
		providerRow, err := data.GetProviderOrderByTradeIDAndProvider(parent.TradeId, mdb.PaymentProviderOkPay)
		if err != nil {
			return nil, err
		}
		if providerRow.ID == 0 || strings.TrimSpace(providerRow.PayURL) == "" {
			return nil, constant.SystemErr
		}
		if err := data.SelectCheckoutOrder(dao.Mdb, parent.TradeId, parent, true); err != nil {
			return nil, err
		}
		resp := buildCheckoutResponse(parent)
		resp.PaymentUrl = providerRow.PayURL
		return resp, nil
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
		if err := data.SelectCheckoutOrder(dao.Mdb, parent.TradeId, existing, true); err != nil {
			return nil, err
		}
		resp := buildCheckoutResponse(existing)
		resp.PaymentUrl = providerRow.PayURL
		return resp, nil
	}

	count, err := data.CountSubOrders(parent.TradeId)
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
		NotifyUrl:       "",
		RedirectUrl:     parent.RedirectUrl,
		Name:            parent.Name,
		EpayType:        parent.EpayType,
		CallBackConfirm: mdb.CallBackConfirmOk,
		PaymentType:     parent.PaymentType,
		PayProvider:     mdb.PaymentProviderOkPay,
		ApiKeyID:        parent.ApiKeyID,

		CallbackSigningKeyID: parent.CallbackSigningKeyID,
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
		return nil, constant.PaymentProviderCreateErr
	}

	finalizeTx := dao.Mdb.Begin()
	if err = data.UpdateProviderOrderCreatedWithTransaction(finalizeTx, subTradeID, mdb.PaymentProviderOkPay, okpayOrder.ProviderOrderID, okpayOrder.PayURL); err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderOkPay)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, err
	}
	if err = data.SelectCheckoutOrder(finalizeTx, parent.TradeId, subOrder, true); err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderOkPay)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, err
	}
	if err = finalizeTx.Commit().Error; err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderOkPay)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, err
	}

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

// completeWaitSelectXLayerOrder completes a status=4 placeholder parent in
// place as an X Layer order: it creates the provider order (fiat amount is
// converted to the token's smallest units), then stores the returned
// paymentIntentId + checkoutUrl. The cashier renders the hosted panel
// because the order carries a payment_url and an empty receive address.
func completeWaitSelectXLayerOrder(parent *mdb.Orders, token string) (*response.CheckoutCounterResponse, error) {
	token = normalizeXLayerToken(token)
	displayAmount, tokenAmount, tokenAmountBaseUnits, err := xlayerAmounts(parent.Amount, parent.Currency)
	if err != nil {
		return nil, err
	}
	// return_url sent to OKX is derived at create time, never admin-entered
	// (2026-11-20 review): prefer the merchant's own redirect_url, else fall
	// back to this order's cashier page. The legacy xlayer.return_url
	// settings key no longer participates — the settings page no longer
	// shows or writes it.
	returnURL := strings.TrimSpace(parent.RedirectUrl)
	if returnURL == "" {
		returnURL = fmt.Sprintf("%s/cashier/%s", config.GetAppUri(), parent.TradeId)
	}
	toAddress := data.GetXLayerToAddress()
	fiatAmount := fmt.Sprintf("%.2f", parent.Amount)
	fiatCurrency := strings.ToUpper(strings.TrimSpace(parent.Currency))
	if fiatCurrency == "" {
		fiatCurrency = "CNY"
	}

	tx := dao.Mdb.Begin()
	providerRow := &mdb.ProviderOrder{
		TradeId:         parent.TradeId,
		Provider:        mdb.PaymentProviderXLayer,
		ReceiveAddress:  toAddress,
		ProviderOrderID: "",
		PayURL:          "",
		Amount:          tokenAmount.InexactFloat64(),
		Coin:            normalizeXLayerToken(token),
		Status:          mdb.ProviderOrderStatusCreating,
		NotifyRaw:       "",
	}
	if err := data.UpsertProviderOrderCreatingWithTransaction(tx, providerRow); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		return nil, err
	}

	xlayerOrder, err := createXLayerOrder(parent.TradeId, toAddress, fiatAmount, fiatCurrency, token, tokenAmountBaseUnits, returnURL, data.GetBrandCashierName(), parent.Name, xlayerExpireSeconds())
	if err != nil {
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderXLayer)
		return nil, constant.PaymentProviderCreateErr
	}

	finalizeTx := dao.Mdb.Begin()
	if err = data.UpdateProviderOrderCreatedWithTransaction(finalizeTx, parent.TradeId, mdb.PaymentProviderXLayer, xlayerOrder.PaymentIntentID, xlayerOrder.CheckoutURL); err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderXLayer)
		return nil, err
	}
	updated, err := data.CompleteWaitSelectXLayerOrderWithTransaction(finalizeTx, parent.TradeId, token, displayAmount)
	if err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderXLayer)
		return nil, err
	}
	if !updated {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderXLayer)
		return nil, constant.OrderStatusConflict
	}
	if err = finalizeTx.Commit().Error; err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(parent.TradeId, mdb.PaymentProviderXLayer)
		return nil, err
	}

	order, err := data.GetOrderInfoByTradeId(parent.TradeId)
	if err != nil {
		return nil, err
	}
	resp := buildCheckoutResponse(order)
	resp.PaymentUrl = xlayerOrder.CheckoutURL
	return resp, nil
}

func switchToXLayer(parent *mdb.Orders, token string) (*response.CheckoutCounterResponse, error) {
	token = normalizeXLayerToken(token)
	if !data.GetXLayerEnabled() {
		return nil, constant.PaymentProviderNotEnabled
	}
	if !data.XLayerConfigured() {
		return nil, constant.PaymentProviderConfigErr
	}
	if !xlayerTokenAllowed(token) {
		return nil, constant.PaymentProviderNotSupport
	}

	if parent.Status == mdb.StatusWaitSelect {
		return completeWaitSelectXLayerOrder(parent, token)
	}

	if parent.PayProvider == mdb.PaymentProviderXLayer && strings.EqualFold(parent.Token, token) {
		providerRow, err := data.GetProviderOrderByTradeIDAndProvider(parent.TradeId, mdb.PaymentProviderXLayer)
		if err != nil {
			return nil, err
		}
		if providerRow.ID == 0 || strings.TrimSpace(providerRow.PayURL) == "" {
			return nil, constant.SystemErr
		}
		if err := data.SelectCheckoutOrder(dao.Mdb, parent.TradeId, parent, true); err != nil {
			return nil, err
		}
		resp := buildCheckoutResponse(parent)
		resp.PaymentUrl = providerRow.PayURL
		return resp, nil
	}

	existing, err := data.GetSubOrderByTokenPayProvider(parent.TradeId, token, mdb.PaymentProviderXLayer)
	if err != nil {
		return nil, err
	}
	if existing.ID > 0 {
		providerRow, err := data.GetProviderOrderByTradeIDAndProvider(existing.TradeId, mdb.PaymentProviderXLayer)
		if err != nil {
			return nil, err
		}
		if providerRow.ID == 0 || strings.TrimSpace(providerRow.PayURL) == "" {
			return nil, constant.SystemErr
		}
		if err := data.SelectCheckoutOrder(dao.Mdb, parent.TradeId, existing, true); err != nil {
			return nil, err
		}
		resp := buildCheckoutResponse(existing)
		resp.PaymentUrl = providerRow.PayURL
		return resp, nil
	}

	count, err := data.CountSubOrders(parent.TradeId)
	if err != nil {
		return nil, err
	}
	if count >= MaxSubOrders {
		return nil, constant.SubOrderLimitExceeded
	}

	displayAmount, tokenAmount, tokenAmountBaseUnits, err := xlayerAmounts(parent.Amount, parent.Currency)
	if err != nil {
		return nil, err
	}

	subTradeID := GenerateCode()
	// return_url sent to OKX is derived at create time, never admin-entered
	// (2026-11-20 review): prefer the merchant's own redirect_url, else fall
	// back to this order's cashier page. The legacy xlayer.return_url
	// settings key no longer participates — the settings page no longer
	// shows or writes it.
	returnURL := strings.TrimSpace(parent.RedirectUrl)
	if returnURL == "" {
		returnURL = fmt.Sprintf("%s/cashier/%s", config.GetAppUri(), parent.TradeId)
	}
	toAddress := data.GetXLayerToAddress()
	fiatAmount := fmt.Sprintf("%.2f", parent.Amount)
	fiatCurrency := strings.ToUpper(strings.TrimSpace(parent.Currency))
	if fiatCurrency == "" {
		fiatCurrency = "CNY"
	}

	tx := dao.Mdb.Begin()
	subOrder := &mdb.Orders{
		TradeId:         subTradeID,
		OrderId:         subTradeID,
		ParentTradeId:   parent.TradeId,
		Amount:          parent.Amount,
		Currency:        parent.Currency,
		ActualAmount:    displayAmount,
		ReceiveAddress:  "",
		Token:           normalizeXLayerToken(token),
		Network:         mdb.PaymentProviderXLayer,
		Status:          mdb.StatusWaitPay,
		NotifyUrl:       "",
		RedirectUrl:     parent.RedirectUrl,
		Name:            parent.Name,
		EpayType:        parent.EpayType,
		CallBackConfirm: mdb.CallBackConfirmOk,
		PaymentType:     parent.PaymentType,
		PayProvider:     mdb.PaymentProviderXLayer,
		ApiKeyID:        parent.ApiKeyID,

		CallbackSigningKeyID: parent.CallbackSigningKeyID,
	}
	if err = data.CreateOrderWithTransaction(tx, subOrder); err != nil {
		tx.Rollback()
		return nil, err
	}
	providerRow := &mdb.ProviderOrder{
		TradeId:         subTradeID,
		Provider:        mdb.PaymentProviderXLayer,
		ReceiveAddress:  toAddress,
		ProviderOrderID: "",
		PayURL:          "",
		Amount:          tokenAmount.InexactFloat64(),
		Coin:            normalizeXLayerToken(token),
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

	xlayerOrder, err := createXLayerOrder(subTradeID, toAddress, fiatAmount, fiatCurrency, token, tokenAmountBaseUnits, returnURL, data.GetBrandCashierName(), parent.Name, xlayerExpireSeconds())
	if err != nil {
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderXLayer)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, constant.PaymentProviderCreateErr
	}

	finalizeTx := dao.Mdb.Begin()
	if err = data.UpdateProviderOrderCreatedWithTransaction(finalizeTx, subTradeID, mdb.PaymentProviderXLayer, xlayerOrder.PaymentIntentID, xlayerOrder.CheckoutURL); err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderXLayer)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, err
	}
	if err = data.SelectCheckoutOrder(finalizeTx, parent.TradeId, subOrder, true); err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderXLayer)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, err
	}
	if err = finalizeTx.Commit().Error; err != nil {
		finalizeTx.Rollback()
		_ = data.MarkProviderOrderFailed(subTradeID, mdb.PaymentProviderXLayer)
		_ = data.ExpireOrderByTradeID(subTradeID)
		return nil, err
	}

	resp := buildCheckoutResponse(subOrder)
	resp.PaymentUrl = xlayerOrder.CheckoutURL
	return resp, nil
}
