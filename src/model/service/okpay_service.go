package service

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/http_client"
	"github.com/GMWalletApp/epusdt/util/log"
)

type okPayCreateDepositResponse struct {
	Status string `json:"status"`
	Code   int    `json:"code"`
	Data   struct {
		OrderID string `json:"order_id"`
		PayURL  string `json:"pay_url"`
	} `json:"data"`
	Msg string `json:"msg"`
}

type okPayDepositOrder struct {
	ProviderOrderID string
	PayURL          string
}

type okPayNotifyPayload struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Code        string `json:"code"`
	OrderID     string `json:"order_id"`
	UniqueID    string `json:"unique_id"`
	PayUserID   string `json:"pay_user_id"`
	Amount      string `json:"amount"`
	Coin        string `json:"coin"`
	PayStatus   string `json:"pay_status"`
	NotifyType  string `json:"notify_type"`
	Sign        string `json:"sign"`
	RawFormData string `json:"raw_form_data"`
}

func okPaySign(form map[string]string, shopID string, shopToken string) map[string]string {
	values := url.Values{}
	for key, value := range form {
		if strings.TrimSpace(value) == "" {
			continue
		}
		values.Set(key, value)
	}
	values.Set("id", shopID)
	query, _ := url.QueryUnescape(values.Encode())
	sum := md5.Sum([]byte(query + "&token=" + shopToken))

	signed := make(map[string]string, len(values)+1)
	for key, vals := range values {
		if len(vals) == 0 {
			continue
		}
		signed[key] = vals[0]
	}
	signed["sign"] = strings.ToUpper(hex.EncodeToString(sum[:]))
	return signed
}

func createOkPayDepositOrder(uniqueID string, amount float64, coin string, returnURL string) (*okPayDepositOrder, error) {
	shopID := data.GetOkPayShopID()
	shopToken := data.GetOkPayShopToken()
	apiURL := strings.TrimSpace(data.GetOkPayAPIURL())
	callbackURL := data.GetOkPayCallbackURL()
	if shopID == "" || shopToken == "" || apiURL == "" || callbackURL == "" {
		return nil, fmt.Errorf("okpay config incomplete")
	}

	if returnURL == "" {
		returnURL = data.GetOkPayReturnURL()
	}

	form := map[string]string{
		"unique_id":    uniqueID,
		"name":         uniqueID,
		"amount":       fmt.Sprintf("%.*f", data.GetAmountPrecision(), amount),
		"coin":         strings.ToUpper(strings.TrimSpace(coin)),
		"callback_url": callbackURL,
		"return_url":   returnURL,
	}
	form = okPaySign(form, shopID, shopToken)

	base, err := url.Parse(apiURL)
	if err != nil {
		return nil, err
	}
	base.Path = path.Join(base.Path, "payLink")

	client := http_client.GetHttpClient()
	client.SetTimeout(time.Duration(data.GetOkPayTimeoutSeconds()) * time.Second)

	var payload okPayCreateDepositResponse
	resp, err := client.R().
		SetFormData(form).
		SetResult(&payload).
		Post(base.String())
	if err != nil {
		log.Sugar.Warnf("[okpay] create order request failed unique_id=%s api_url=%s callback_url=%s return_url=%s err=%v", uniqueID, base.String(), callbackURL, returnURL, err)
		return nil, err
	}
	if resp.StatusCode() >= 400 {
		log.Sugar.Warnf("[okpay] create order http error unique_id=%s status=%s api_url=%s callback_url=%s return_url=%s body=%s", uniqueID, resp.Status(), base.String(), callbackURL, returnURL, resp.String())
		return nil, fmt.Errorf("okpay http status: %s", resp.Status())
	}
	if payload.Data.OrderID == "" || payload.Data.PayURL == "" {
		log.Sugar.Warnf("[okpay] create order rejected unique_id=%s api_url=%s callback_url=%s return_url=%s status=%s code=%d msg=%s", uniqueID, base.String(), callbackURL, returnURL, payload.Status, payload.Code, payload.Msg)
		if payload.Msg != "" {
			return nil, fmt.Errorf("okpay create order failed: %s (callback_url=%s)", payload.Msg, callbackURL)
		}
		return nil, fmt.Errorf("okpay create order failed (callback_url=%s)", callbackURL)
	}

	return &okPayDepositOrder{
		ProviderOrderID: payload.Data.OrderID,
		PayURL:          payload.Data.PayURL,
	}, nil
}

func verifyOkPayNotify(form map[string]string) bool {
	shopID := data.GetOkPayShopID()
	shopToken := data.GetOkPayShopToken()
	signature := strings.TrimSpace(form["sign"])
	if shopID == "" || shopToken == "" || signature == "" {
		return false
	}

	if expected := okPayNotifySign(form, shopToken); expected != "" && strings.EqualFold(expected, signature) {
		return true
	}

	unsigned := make(map[string]string, len(form))
	for key, value := range form {
		if strings.EqualFold(key, "sign") {
			continue
		}
		unsigned[key] = value
	}
	signed := okPaySign(unsigned, shopID, shopToken)
	return strings.EqualFold(strings.TrimSpace(signed["sign"]), signature)
}

func okPayNotifySign(form map[string]string, shopToken string) string {
	orderedKeys := []string{
		"code",
		"data[order_id]",
		"data[unique_id]",
		"data[pay_user_id]",
		"data[amount]",
		"data[coin]",
		"data[status]",
		"data[type]",
		"id",
		"status",
	}

	pairs := make([]string, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		value := strings.TrimSpace(form[key])
		if value == "" {
			continue
		}
		pairs = append(pairs, url.QueryEscape(key)+"="+url.QueryEscape(value))
	}
	if len(pairs) == 0 {
		return ""
	}

	query, err := url.QueryUnescape(strings.Join(pairs, "&"))
	if err != nil {
		return ""
	}
	sum := md5.Sum([]byte(query + "&token=" + shopToken))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func parseOkPayNotify(form map[string]string, rawFormData string) *okPayNotifyPayload {
	return &okPayNotifyPayload{
		ID:          strings.TrimSpace(form["id"]),
		Status:      strings.TrimSpace(form["status"]),
		Code:        strings.TrimSpace(form["code"]),
		OrderID:     strings.TrimSpace(form["data[order_id]"]),
		UniqueID:    strings.TrimSpace(form["data[unique_id]"]),
		PayUserID:   strings.TrimSpace(form["data[pay_user_id]"]),
		Amount:      strings.TrimSpace(form["data[amount]"]),
		Coin:        strings.TrimSpace(form["data[coin]"]),
		PayStatus:   strings.TrimSpace(form["data[status]"]),
		NotifyType:  strings.TrimSpace(form["data[type]"]),
		Sign:        strings.TrimSpace(form["sign"]),
		RawFormData: rawFormData,
	}
}

func HandleOkPayNotify(form map[string]string, rawFormData string) error {
	if !verifyOkPayNotify(form) {
		return fmt.Errorf("invalid okpay notify sign")
	}

	payload := parseOkPayNotify(form, rawFormData)

	if payload.ID == "" || payload.ID != data.GetOkPayShopID() {
		return fmt.Errorf("okpay notify shop id mismatch: %s", payload.ID)
	}
	if payload.UniqueID == "" || payload.OrderID == "" {
		return fmt.Errorf("missing okpay notify identifiers")
	}

	order, err := data.GetOrderInfoByTradeId(payload.UniqueID)
	if err != nil {
		return err
	}
	if order.ID <= 0 {
		return fmt.Errorf("okpay notify order not found: %s", payload.UniqueID)
	}
	if order.PayProvider != "okpay" {
		return fmt.Errorf("okpay notify order provider mismatch: %s", payload.UniqueID)
	}

	providerRow, err := data.GetProviderOrderByTradeIDAndProvider(payload.UniqueID, order.PayProvider)
	if err != nil {
		return err
	}
	if providerRow.ID <= 0 {
		return fmt.Errorf("okpay provider order not found: %s", payload.UniqueID)
	}

	_ = data.SaveProviderOrderNotify(payload.UniqueID, order.PayProvider, payload.RawFormData)

	if !strings.EqualFold(payload.Status, "success") || !strings.EqualFold(payload.NotifyType, "deposit") || payload.PayStatus != "1" {
		log.Sugar.Infof("[okpay] notify ignored trade_id=%s status=%s pay_status=%s type=%s", payload.UniqueID, payload.Status, payload.PayStatus, payload.NotifyType)
		return nil
	}

	if providerRow.ProviderOrderID != "" && providerRow.ProviderOrderID != payload.OrderID {
		return fmt.Errorf("okpay provider order id mismatch: got=%s want=%s", payload.OrderID, providerRow.ProviderOrderID)
	}
	if !strings.EqualFold(strings.TrimSpace(order.Token), strings.TrimSpace(payload.Coin)) {
		return fmt.Errorf("okpay coin mismatch: got=%s want=%s", payload.Coin, order.Token)
	}

	notifyAmount, err := strconv.ParseFloat(payload.Amount, 64)
	if err != nil {
		return fmt.Errorf("invalid okpay amount: %w", err)
	}
	precision := data.GetAmountPrecision()
	if fmt.Sprintf("%.*f", precision, notifyAmount) != fmt.Sprintf("%.*f", precision, order.ActualAmount) {
		return fmt.Errorf("okpay amount mismatch: got=%.*f want=%.*f", precision, notifyAmount, precision, order.ActualAmount)
	}

	err = OrderProcessing(&request.OrderProcessingRequest{
		ReceiveAddress:     order.ReceiveAddress,
		Currency:           order.Currency,
		Token:              order.Token,
		Network:            order.Network,
		Amount:             order.ActualAmount,
		TradeId:            order.TradeId,
		BlockTransactionId: payload.OrderID,
	})
	processed := err == nil
	if err != nil && err != constant.OrderBlockAlreadyProcess {
		return err
	}

	if processed {
		sendPaymentNotification(order)
	}
	if err = data.MarkProviderOrderPaid(payload.UniqueID, order.PayProvider, payload.RawFormData); err != nil {
		return err
	}
	return nil
}
