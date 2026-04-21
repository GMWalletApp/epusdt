package service

import (
	"errors"

	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/response"
)

var ErrOrderNotFound = errors.New("订单不存在")

// GetCheckoutCounterByTradeId returns checkout info for a pending order.
func GetCheckoutCounterByTradeId(tradeId string) (*response.CheckoutCounterResponse, error) {
	orderInfo, err := data.GetOrderInfoByTradeId(tradeId)
	if err != nil {
		return nil, err
	}
	if orderInfo.ID <= 0 {
		return nil, ErrOrderNotFound
	}
	if orderInfo.Status != mdb.StatusWaitPay {
		return buildCheckoutResponse(orderInfo)
	}
	rootTradeId := orderInfo.TradeId
	if orderInfo.ParentTradeId != "" {
		rootTradeId = orderInfo.ParentTradeId
	}
	selectedOrder, err := data.GetSelectedOrderInFamily(rootTradeId)
	if err != nil {
		return nil, err
	}
	if selectedOrder.ID > 0 {
		orderInfo = selectedOrder
	}
	return buildCheckoutResponse(orderInfo)
}
