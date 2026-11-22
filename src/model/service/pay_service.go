package service

import (
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/response"
	"github.com/GMWalletApp/epusdt/util/constant"
)

// ErrOrder is returned when checkout initialization cannot find the trade id.
var ErrOrder = constant.OrderNotExists

// An expired payment attempt can belong to a completed checkout. Preserve
// every other attempt state, including active confirmation and manual review.
func settledCheckoutOrder(order *mdb.Orders) (*mdb.Orders, error) {
	if order.Status != mdb.StatusExpired || order.ParentTradeId == "" {
		return order, nil
	}
	parent, err := data.GetOrderInfoByTradeId(order.ParentTradeId)
	if err != nil {
		return nil, err
	}
	if parent.ID != 0 && parent.Status == mdb.StatusPaySuccess {
		return parent, nil
	}
	return order, nil
}

func GetPaymentStatus(tradeID string, checkout bool) (*response.CheckStatusResponse, error) {
	order, err := GetOrderInfoByTradeId(tradeID)
	if err != nil {
		return nil, err
	}
	var redirect string
	if checkout {
		resolved, err := settledCheckoutOrder(order)
		if err != nil {
			return nil, err
		}
		if resolved.TradeId != order.TradeId {
			redirect = buildPublicRedirectURL(resolved)
		}
		order = resolved
	}
	return &response.CheckStatusResponse{TradeId: order.TradeId, Status: order.Status, RedirectUrl: redirect}, nil
}

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
	orderInfo, err = settledCheckoutOrder(orderInfo)
	if err != nil {
		return nil, err
	}
	if orderInfo.ParentTradeId == "" && orderInfo.Status == mdb.StatusWaitPay {
		child, err := data.SelectedCheckoutChild(orderInfo.TradeId)
		if err != nil {
			return nil, err
		}
		if child.ID != 0 {
			orderInfo = child
		}
	}
	resp := buildCheckoutResponse(orderInfo)
	if orderInfo.PayProvider == mdb.PaymentProviderOkPay || orderInfo.PayProvider == mdb.PaymentProviderXLayer {
		providerRow, rowErr := data.GetProviderOrderByTradeIDAndProvider(orderInfo.TradeId, orderInfo.PayProvider)
		if rowErr != nil {
			return nil, rowErr
		}
		if providerRow.ID > 0 {
			resp.PaymentUrl = providerRow.PayURL
		}
	}
	return resp, nil
}
