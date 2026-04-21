package comm

import (
	"encoding/json"
	"html/template"
	"net/http"
	"path/filepath"

	"github.com/assimon/luuu/config"
	"github.com/assimon/luuu/model/response"
	"github.com/assimon/luuu/model/service"
	"github.com/labstack/echo/v4"
)

// CheckoutCounter 收银台
func (c *BaseCommController) CheckoutCounter(ctx echo.Context) (err error) {
	type pageData struct {
		response.CheckoutCounterResponse
		PaymentOptionsJSON template.JS
	}

	buildPageData := func(resp response.CheckoutCounterResponse) pageData {
		paymentOptionsJSON := template.JS("[]")
		if len(resp.PaymentOptions) > 0 {
			if b, err := json.Marshal(resp.PaymentOptions); err == nil {
				paymentOptionsJSON = template.JS(string(b))
			}
		}
		return pageData{
			CheckoutCounterResponse: resp,
			PaymentOptionsJSON:      paymentOptionsJSON,
		}
	}

	tradeId := ctx.Param("trade_id")
	resp, err := service.GetCheckoutCounterByTradeId(tradeId)
	if err != nil {
		if err == service.ErrOrderNotFound {
			tmpl, err := template.ParseFiles(filepath.Join(config.StaticFilePath, "index.html"))
			if err != nil {
				return ctx.String(http.StatusOK, err.Error())
			}
			return tmpl.Execute(ctx.Response(), buildPageData(response.CheckoutCounterResponse{}))
		}
		return ctx.String(http.StatusOK, err.Error())
	}
	tmpl, err := template.ParseFiles(filepath.Join(config.StaticFilePath, "index.html"))
	if err != nil {
		return ctx.String(http.StatusOK, err.Error())
	}

	return tmpl.Execute(ctx.Response(), buildPageData(*resp))
}

// CheckStatus 支付状态检测
func (c *BaseCommController) CheckStatus(ctx echo.Context) (err error) {
	tradeId := ctx.Param("trade_id")
	order, err := service.GetOrderInfoByTradeId(tradeId)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	resp := response.CheckStatusResponse{
		TradeId: order.TradeId,
		Status:  order.Status,
	}
	return c.SucJson(ctx, resp)
}
