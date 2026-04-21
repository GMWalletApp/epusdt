package service

import (
	"strings"

	"github.com/assimon/luuu/config"
	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/response"
	"github.com/assimon/luuu/util/constant"
	"github.com/shopspring/decimal"
)

func ensurePaymentMethodAvailable(network, token string) error {
	enabled, err := data.IsSupportedAssetEnabled(network, token)
	if err != nil {
		return err
	}
	if !enabled {
		return constant.PaymentMethodUnavailable
	}
	return nil
}

func buildCheckoutPaymentOptions(order *mdb.Orders) ([]response.CheckoutPaymentOption, error) {
	rootTradeId := order.TradeId
	if order.ParentTradeId != "" {
		rootTradeId = order.ParentTradeId
	}

	rootOrder, err := data.GetOrderInfoByTradeId(rootTradeId)
	if err != nil {
		return nil, err
	}
	if rootOrder.ID <= 0 {
		return nil, constant.OrderNotExists
	}

	activeSubOrders, err := data.GetActiveSubOrders(rootTradeId)
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	options := make([]response.CheckoutPaymentOption, 0, 1+len(activeSubOrders))
	appendOption := func(network, token string) {
		network = strings.ToLower(strings.TrimSpace(network))
		token = strings.ToUpper(strings.TrimSpace(token))
		if network == "" || token == "" {
			return
		}
		key := network + ":" + token
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		options = append(options, response.CheckoutPaymentOption{
			Token:   token,
			Network: network,
		})
	}

	appendOption(rootOrder.Network, rootOrder.Token)
	for _, subOrder := range activeSubOrders {
		appendOption(subOrder.Network, subOrder.Token)
	}

	if len(activeSubOrders) >= MaxSubOrders {
		return options, nil
	}

	assets, err := data.ListEnabledSupportedAssets()
	if err != nil {
		return nil, err
	}
	wallets, err := data.GetAvailableWalletAddress()
	if err != nil {
		return nil, err
	}

	networksWithWallet := make(map[string]struct{}, len(wallets))
	for _, wallet := range wallets {
		network := strings.ToLower(strings.TrimSpace(wallet.Network))
		if network == "" {
			continue
		}
		networksWithWallet[network] = struct{}{}
	}

	for _, asset := range assets {
		network := strings.ToLower(strings.TrimSpace(asset.Network))
		if _, ok := networksWithWallet[network]; !ok {
			continue
		}
		rate := config.GetRateForCoin(strings.ToLower(asset.Token), strings.ToLower(rootOrder.Currency))
		if rate <= 0 {
			continue
		}
		decimalTokenAmount := decimal.NewFromFloat(rootOrder.Amount).Mul(decimal.NewFromFloat(rate))
		if decimalTokenAmount.Cmp(decimal.NewFromFloat(UsdtMinimumPaymentAmount)) == -1 {
			continue
		}
		appendOption(network, asset.Token)
	}

	return options, nil
}

func buildCheckoutResponse(order *mdb.Orders) (*response.CheckoutCounterResponse, error) {
	if order.Status != mdb.StatusWaitPay {
		return &response.CheckoutCounterResponse{
			TradeId:        order.TradeId,
			Amount:         order.Amount,
			ActualAmount:   order.ActualAmount,
			Token:          order.Token,
			Currency:       order.Currency,
			ReceiveAddress: order.ReceiveAddress,
			Network:        order.Network,
			Status:         order.Status,
			ExpirationTime: order.CreatedAt.AddMinutes(config.GetOrderExpirationTime()).TimestampMilli(),
			RedirectUrl:    order.RedirectUrl,
			CreatedAt:      order.CreatedAt.TimestampMilli(),
			IsSelected:     order.IsSelected,
		}, nil
	}
	paymentOptions, err := buildCheckoutPaymentOptions(order)
	if err != nil {
		return nil, err
	}
	return &response.CheckoutCounterResponse{
		TradeId:        order.TradeId,
		Amount:         order.Amount,
		ActualAmount:   order.ActualAmount,
		Token:          order.Token,
		Currency:       order.Currency,
		ReceiveAddress: order.ReceiveAddress,
		Network:        order.Network,
		Status:         order.Status,
		ExpirationTime: order.CreatedAt.AddMinutes(config.GetOrderExpirationTime()).TimestampMilli(),
		RedirectUrl:    order.RedirectUrl,
		CreatedAt:      order.CreatedAt.TimestampMilli(),
		IsSelected:     order.IsSelected,
		PaymentOptions: paymentOptions,
	}, nil
}
