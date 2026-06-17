package mq

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/response"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/sign"
)

// resolveOrderApiKey returns the api_keys row that signed the order.
// Every order must carry an ApiKeyID (the default key is always seeded
// at bootstrap, and the middleware/EPAY inline flow always stamps it).
// If the row is missing or disabled, we return an error — using any
// other secret would produce a signature the merchant can't verify.
// The admin can resend the callback after fixing the key.
func resolveOrderApiKey(order *mdb.Orders) (*mdb.ApiKey, error) {
	row, err := service.ResolveOrderApiKey(order)
	if err != nil || row == nil || row.ID == 0 {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("api key unavailable")
	}
	return row, nil
}

const batchSize = 100

const sqliteBusyRetryAttempts = 3

type expirableOrder struct {
	ID             uint64  `gorm:"column:id"`
	TradeId        string  `gorm:"column:trade_id"`
	Status         int     `gorm:"column:status"`
	Network        string  `gorm:"column:network"`
	ReceiveAddress string  `gorm:"column:receive_address"`
	Token          string  `gorm:"column:token"`
	ActualAmount   float64 `gorm:"column:actual_amount"`
	PayProvider    string  `gorm:"column:pay_provider"`
}

func runOrderExpirationLoop() {
	runLoop("order_expiration", processExpiredOrders)
}

func runOrderCallbackLoop() {
	runLoop("order_callback", dispatchPendingCallbacks)
}

func runTransactionLockCleanupLoop() {
	runLoop("transaction_lock_cleanup", cleanupExpiredTransactionLocks)
}

func runLoop(name string, fn func()) {
	safeRun(name, fn)
	ticker := time.NewTicker(config.GetQueuePollInterval())
	defer ticker.Stop()

	for range ticker.C {
		safeRun(name, fn)
	}
}

func safeRun(name string, fn func()) {
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[mq] %s panic: %v", name, err)
		}
	}()
	fn()
}

func processExpiredOrders() {
	expirationCutoff := time.Now().Add(-config.GetOrderExpirationTimeDuration())
	for {
		var orders []expirableOrder
		err := withSQLiteBusyRetry(func() error {
			return dao.Mdb.Model(&mdb.Orders{}).
				Select("id", "trade_id", "status", "network", "receive_address", "token", "actual_amount", "pay_provider").
				Where("status IN ?", []int{mdb.StatusWaitPay, mdb.StatusWaitSelect}).
				Where("created_at <= ?", expirationCutoff).
				Order("id asc").
				Limit(batchSize).
				Find(&orders).Error
		})
		if err != nil {
			log.Sugar.Errorf("[mq] query expired orders failed: %v", err)
			return
		}
		if len(orders) == 0 {
			return
		}

		for _, order := range orders {
			expired, err := data.UpdateOrderIsExpirationById(order.ID, expirationCutoff)
			if err != nil {
				log.Sugar.Errorf("[mq] expire order failed, trade_id=%s, err=%v", order.TradeId, err)
				continue
			}
			if !expired {
				continue
			}
			// StatusWaitSelect placeholders have not reserved a wallet or
			// amount yet, so there is no transaction lock to release.
			if order.Status == mdb.StatusWaitSelect {
				continue
			}
			if strings.TrimSpace(order.PayProvider) != "" && order.PayProvider != mdb.PaymentProviderOnChain {
				continue
			}
			if strings.TrimSpace(order.Network) == "" || strings.TrimSpace(order.ReceiveAddress) == "" || strings.TrimSpace(order.Token) == "" || order.ActualAmount <= 0 {
				continue
			}
			if err = data.UnLockTransaction(order.Network, order.ReceiveAddress, order.Token, order.ActualAmount); err != nil {
				log.Sugar.Warnf("[mq] release expired transaction lock failed, trade_id=%s, err=%v", order.TradeId, err)
			}
		}

		if len(orders) < batchSize {
			return
		}
	}
}

func dispatchPendingCallbacks() {
	maxRetry := config.GetOrderNoticeMaxRetry()
	var orders []data.PendingCallbackOrder
	err := withSQLiteBusyRetry(func() error {
		var innerErr error
		orders, innerErr = data.GetPendingCallbackOrders(maxRetry, batchSize)
		return innerErr
	})
	if err != nil {
		log.Sugar.Errorf("[mq] query callback orders failed: %v", err)
		return
	}

	now := time.Now()
	for _, order := range orders {
		if !isCallbackDue(&order, now, maxRetry) {
			continue
		}
		tradeID := order.TradeId
		if _, loaded := callbackInflight.LoadOrStore(tradeID, struct{}{}); loaded {
			continue
		}

		select {
		case callbackLimiter <- struct{}{}:
			go processCallback(tradeID)
		default:
			callbackInflight.Delete(tradeID)
			return
		}
	}
}

func processCallback(tradeID string) {
	defer func() {
		<-callbackLimiter
		callbackInflight.Delete(tradeID)
	}()

	freshOrder, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Errorf("[mq] reload callback order failed, trade_id=%s, err=%v", tradeID, err)
		return
	}
	if freshOrder.ID <= 0 || freshOrder.Status != mdb.StatusPaySuccess || freshOrder.CallBackConfirm != mdb.CallBackConfirmNo {
		return
	}

	if err = sendOrderCallback(freshOrder); err != nil {
		log.Sugar.Warnf("[mq] callback request failed, trade_id=%s, attempt=%d, err=%v", freshOrder.TradeId, freshOrder.CallbackNum+1, err)
		freshOrder.CallBackConfirm = mdb.CallBackConfirmNo
	} else {
		freshOrder.CallBackConfirm = mdb.CallBackConfirmOk
	}

	if err = data.SaveCallBackOrdersResp(freshOrder); err != nil {
		log.Sugar.Errorf("[mq] save callback result failed, trade_id=%s, err=%v", freshOrder.TradeId, err)
	}
}

func sendOrderCallback(order *mdb.Orders) error {
	apiKeyRow, err := resolveOrderApiKey(order)
	if err != nil || apiKeyRow == nil || apiKeyRow.ID == 0 {
		return errors.New("no api key row available for callback")
	}

	switch {
	case strings.EqualFold(order.PaymentType, mdb.PaymentTypeEpay):
		formData, err := service.BuildEPayResultParams(order, apiKeyRow)
		if err != nil {
			return err
		}

		epayResp, err := http_client.GetHttpClient().R().SetQueryParams(formData).Get(order.NotifyUrl)
		if err != nil {
			return err
		}
		log.Sugar.Infof("[mq] epay notify_url response status: %d, body: %s", epayResp.StatusCode(), string(epayResp.Body()))
		if epayResp.StatusCode() != http.StatusOK {
			return errors.New(epayResp.Status())
		}
		if !isCallbackAck(epayResp.Body()) {
			return errors.New("not ok")
		}

	default:

		client := http_client.GetHttpClient()
		orderResp := response.OrderNotifyResponse{
			Pid:                apiKeyRow.Pid,
			TradeId:            order.TradeId,
			OrderId:            order.OrderId,
			Amount:             order.Amount,
			ActualAmount:       order.ActualAmount,
			ReceiveAddress:     order.ReceiveAddress,
			Token:              order.Token,
			BlockTransactionId: order.BlockTransactionId,
			Status:             mdb.StatusPaySuccess,
		}
		signature, err := sign.Get(orderResp, apiKeyRow.SecretKey)
		if err != nil {
			return err
		}
		orderResp.Signature = signature

		resp, err := client.R().
			SetHeader("powered-by", "Epusdt(https://github.com/GMwalletApp/epusdt)").
			SetBody(orderResp).
			Post(order.NotifyUrl)
		if err != nil {
			return err
		}
		if resp.StatusCode() != http.StatusOK {
			return errors.New(resp.Status())
		}
		if !isCallbackAck(resp.Body()) {
			return errors.New("not ok")
		}
	}

	return nil
}

func isCallbackAck(body []byte) bool {
	ack := strings.ToLower(strings.TrimSpace(string(body)))
	return ack == "ok" || ack == "success"
}

func cleanupExpiredTransactionLocks() {
	if err := data.CleanupExpiredTransactionLocks(); err != nil {
		log.Sugar.Errorf("[mq] cleanup expired transaction locks failed: %v", err)
	}
}

func withSQLiteBusyRetry(fn func() error) error {
	var err error
	for attempt := 1; attempt <= sqliteBusyRetryAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if !isSQLiteBusyError(err) || attempt == sqliteBusyRetryAttempts {
			return err
		}
		time.Sleep(time.Duration(attempt*25) * time.Millisecond)
	}
	return err
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

func isCallbackDue(order *data.PendingCallbackOrder, now time.Time, maxRetry int) bool {
	if order.CallBackConfirm != mdb.CallBackConfirmNo {
		return false
	}
	if order.CallbackNum > maxRetry {
		return false
	}
	if order.CallbackNum == 0 {
		return true
	}
	nextRunAt := order.UpdatedAt.StdTime().Add(callbackRetryDelay(order.CallbackNum))
	return !nextRunAt.After(now)
}

func callbackRetryDelay(attempts int) time.Duration {
	if attempts <= 0 {
		return 0
	}

	delay := config.GetCallbackRetryBaseDuration()
	maxDelay := 5 * time.Minute
	for i := 1; i < attempts; i++ {
		if delay >= maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}
