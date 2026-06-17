package service

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/response"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/sign"
)

type EPayReturnRedirect struct {
	TargetURL          string
	IsMerchantRedirect bool
}

func isEPayOrder(order *mdb.Orders) bool {
	return order != nil && strings.EqualFold(order.PaymentType, mdb.PaymentTypeEpay)
}

func buildCheckoutCounterPath(tradeID string) string {
	return "/pay/checkout-counter/" + tradeID
}

func buildAbsoluteAppURL(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	base := strings.TrimRight(strings.TrimSpace(config.GetAppUri()), "/")
	if base == "" {
		return path
	}
	return base + path
}

func buildEPayReturnPath(tradeID string) string {
	return "/pay/return/" + tradeID
}

func buildPublicRedirectURL(order *mdb.Orders) string {
	if order == nil {
		return ""
	}
	raw := strings.TrimSpace(order.RedirectUrl)
	if raw == "" || !isEPayOrder(order) {
		return raw
	}
	return buildAbsoluteAppURL(buildEPayReturnPath(order.TradeId))
}

func ResolveOrderApiKey(order *mdb.Orders) (*mdb.ApiKey, error) {
	if order == nil || order.ApiKeyID == 0 {
		return nil, constant.OrderApiKeyUnavailableErr
	}
	row, err := data.GetApiKeyByID(order.ApiKeyID)
	if err != nil {
		return nil, err
	}
	if row == nil || row.ID == 0 || row.Status != mdb.ApiKeyStatusEnable {
		return nil, constant.OrderApiKeyUnavailableErr
	}
	return row, nil
}

func BuildEPayResultParams(order *mdb.Orders, apiKeyRow *mdb.ApiKey) (map[string]string, error) {
	if order == nil || apiKeyRow == nil {
		return nil, constant.EPayReturnSignatureErr
	}

	pidInt, err := strconv.Atoi(strings.TrimSpace(apiKeyRow.Pid))
	if err != nil {
		return nil, constant.EPayReturnSignatureErr
	}

	notifyData := response.OrderNotifyResponseEpay{
		PID:         pidInt,
		TradeNo:     order.TradeId,
		OutTradeNo:  order.OrderId,
		Type:        "alipay",
		Name:        order.Name,
		Money:       fmt.Sprintf("%.4f", order.Amount),
		TradeStatus: "TRADE_SUCCESS",
	}

	signstr, err := sign.Get(notifyData, apiKeyRow.SecretKey)
	if err != nil {
		return nil, constant.EPayReturnSignatureErr
	}

	return map[string]string{
		"pid":          strconv.Itoa(pidInt),
		"trade_no":     notifyData.TradeNo,
		"out_trade_no": notifyData.OutTradeNo,
		"type":         notifyData.Type,
		"name":         notifyData.Name,
		"money":        notifyData.Money,
		"trade_status": notifyData.TradeStatus,
		"sign":         signstr,
		"sign_type":    "MD5",
	}, nil
}

func appendQueryParams(rawURL string, params map[string]string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", constant.OrderRedirectURLErr
	}

	targetURL, err := url.Parse(rawURL)
	if err != nil {
		return "", constant.OrderRedirectURLErr
	}

	query := targetURL.Query()
	for key, value := range params {
		query.Set(key, value)
	}
	targetURL.RawQuery = query.Encode()
	return targetURL.String(), nil
}

func ResolveEPayReturnRedirect(tradeID string) (*EPayReturnRedirect, error) {
	order, err := GetOrderInfoByTradeId(tradeID)
	if err != nil {
		return nil, err
	}

	if !isEPayOrder(order) || order.Status != mdb.StatusPaySuccess {
		return &EPayReturnRedirect{
			TargetURL: buildCheckoutCounterPath(order.TradeId),
		}, nil
	}

	rawMerchantURL := strings.TrimSpace(order.RedirectUrl)
	if rawMerchantURL == "" {
		return nil, constant.OrderRedirectURLErr
	}

	apiKeyRow, err := ResolveOrderApiKey(order)
	if err != nil {
		return nil, err
	}

	params, err := BuildEPayResultParams(order, apiKeyRow)
	if err != nil {
		return nil, err
	}

	targetURL, err := appendQueryParams(rawMerchantURL, params)
	if err != nil {
		return nil, err
	}

	return &EPayReturnRedirect{
		TargetURL:          targetURL,
		IsMerchantRedirect: true,
	}, nil
}
