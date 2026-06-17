package service

import (
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/response"
	"github.com/GMWalletApp/epusdt/util/constant"
)

// ErrOrder is returned when checkout initialization cannot find the trade id.
var ErrOrder = constant.OrderNotExists

// GetCheckoutCounterByTradeId returns checkout initialization data for an existing order.
// It does not decide the payment state; callers should use CheckStatus for that.
func GetCheckoutCounterByTradeId(tradeId string) (*response.CheckoutCounterResponse, error) {
	orderInfo, err := data.GetOrderInfoByTradeId(tradeId)
	if err != nil {
		return nil, err
	}
	if orderInfo.ID <= 0 {
		return nil, ErrOrder
	}
	resp := buildCheckoutResponse(orderInfo)
	if orderInfo.PayProvider == mdb.PaymentProviderOkPay {
		providerRow, rowErr := data.GetProviderOrderByTradeIDAndProvider(orderInfo.TradeId, mdb.PaymentProviderOkPay)
		if rowErr != nil {
			return nil, rowErr
		}
		if providerRow.ID > 0 {
			resp.PaymentUrl = providerRow.PayURL
		}
	}
	return resp, nil
}
