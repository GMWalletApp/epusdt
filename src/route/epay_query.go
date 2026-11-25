package route

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/GMWalletApp/epusdt/middleware"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func epayOrderQuery(ctx echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "no-store")
	values := make(url.Values)
	for key, items := range ctx.QueryParams() {
		values[key] = append([]string(nil), items...)
	}
	if ctx.Request().Method == http.MethodPost {
		ctx.Request().Body = http.MaxBytesReader(ctx.Response(), ctx.Request().Body, 64<<10)
		if err := ctx.Request().ParseForm(); err != nil {
			return ctx.JSON(http.StatusOK, map[string]interface{}{"code": 0, "msg": "invalid request"})
		}
		for key, items := range ctx.Request().PostForm {
			if existing, ok := values[key]; ok && strings.Join(existing, "\x00") != strings.Join(items, "\x00") {
				return ctx.JSON(http.StatusOK, map[string]interface{}{"code": 0, "msg": "invalid request"})
			}
			values[key] = append([]string(nil), items...)
		}
	}
	get := func(key string) (string, bool) {
		items, ok := values[key]
		if !ok || len(items) != 1 {
			return "", false
		}
		return strings.TrimSpace(items[0]), true
	}
	act, actOK := get("act")
	pid, pidOK := get("pid")
	secret, secretOK := get("key")
	orderID, orderOK := get("out_trade_no")
	tradeID, tradeOK := get("trade_no")
	apiKey, err := data.GetEnabledApiKey(pid)
	if !actOK || act != "order" || !pidOK || !secretOK || !orderOK && !tradeOK || orderOK && orderID == "" || tradeOK && tradeID == "" || err != nil || apiKey == nil || apiKey.ID == 0 || subtle.ConstantTimeCompare([]byte(secret), []byte(apiKey.SecretKey)) != 1 || !middleware.IsIPWhitelisted(apiKey.IpWhitelist, ctx.RealIP()) {
		return ctx.JSON(http.StatusOK, map[string]interface{}{"code": 0, "msg": "invalid order query"})
	}
	_ = data.TouchApiKeyUsage(apiKey.ID)
	lookup := func(column, value string) (*mdb.Orders, error) {
		if value == "" {
			return nil, nil
		}
		var order mdb.Orders
		result := epayOrderLookup(apiKey.ID, column, value).Limit(1).Find(&order)
		return &order, result.Error
	}
	var order *mdb.Orders
	if orderOK {
		order, err = lookup("order_id", orderID)
		if err == nil && (order == nil || order.ID == 0) {
			err = fmt.Errorf("order not found")
		}
	}
	if err == nil && tradeOK {
		var byTradeID *mdb.Orders
		byTradeID, err = lookup("trade_id", tradeID)
		if err == nil && (byTradeID == nil || byTradeID.ID == 0) {
			err = fmt.Errorf("order not found")
		}
		if err == nil && order != nil && order.ID != 0 && byTradeID != nil && byTradeID.ID != 0 && order.ID != byTradeID.ID {
			err = fmt.Errorf("order identifiers do not match")
		}
		if order == nil || order.ID == 0 {
			order = byTradeID
		}
	}
	if err != nil || order == nil || order.ID == 0 {
		return ctx.JSON(http.StatusOK, map[string]interface{}{"code": 0, "msg": "order not found"})
	}
	status, tradeStatus := 0, "WAIT_BUYER_PAY"
	if order.Status == mdb.StatusPaySuccess {
		status, tradeStatus = 1, "TRADE_SUCCESS"
	}
	epayType := strings.TrimSpace(order.EpayType)
	if epayType == "" {
		epayType = "alipay"
	}
	return ctx.JSON(http.StatusOK, map[string]interface{}{
		"code": 1, "msg": "success", "pid": apiKey.Pid,
		"trade_no": order.TradeId, "out_trade_no": order.OrderId,
		"type": epayType, "money": fmt.Sprintf("%.4f", order.Amount),
		"status": status, "trade_status": tradeStatus,
	})
}

func epayOrderLookup(apiKeyID uint64, column, value string) *gorm.DB {
	return dao.Mdb.Where("api_key_id = ? AND parent_trade_id = '' AND lower(payment_type) = ?", apiKeyID, strings.ToLower(mdb.PaymentTypeEpay)).Where(column+" = ?", value)
}
