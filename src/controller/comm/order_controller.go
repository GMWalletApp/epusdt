package comm

import (
	"encoding/json"
	"net/http"

	"github.com/GMWalletApp/epusdt/middleware"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/labstack/echo/v4"
)

// apiKeyFromContext returns the api_keys row stamped by CheckApiSign.
// Returns nil when the middleware didn't run (should not happen on authed routes).
func apiKeyFromContext(ctx echo.Context) *mdb.ApiKey {
	if v, ok := ctx.Get(middleware.ApiKeyRowKey).(*mdb.ApiKey); ok {
		return v
	}
	return nil
}

// CreateTransaction 创建交易
// @Summary      Create transaction
// @Description  Create a payment transaction order. Accepts JSON body (application/json) or form-encoded body (application/x-www-form-urlencoded).
// @Description  GMPay may omit both token and network to create a status=4 placeholder order; EPay submit.php can also create one when neither request parameters nor database defaults provide token/network. Supplying only one of token/network is invalid.
// @Description  payment_type is optional for GMPay. If it is sent, it is a normal signed parameter and must be included when calculating signature.
// @Tags         Payment
// @Accept       json
// @Accept       x-www-form-urlencoded
// @Produce      json
// @Param        request body request.CreateTransactionRequest false "Transaction payload (JSON)"
// @Param        order_id formData string false "Merchant order ID"
// @Param        currency formData string false "Fiat currency (e.g. cny)"
// @Param        token formData string false "Crypto token (e.g. TON, USDT); omit together with network to create a placeholder where supported"
// @Param        network formData string false "Network (e.g. ton, tron); omit together with token to create a placeholder where supported"
// @Param        amount formData number false "Amount"
// @Param        notify_url formData string false "Callback URL"
// @Param        signature formData string false "MD5 signature"
// @Param        redirect_url formData string false "Redirect URL"
// @Param        name formData string false "Order name"
// @Param        payment_type formData string false "Optional GMPay compatibility flag; include in signature when sent"
// @Success      200 {object} response.ApiResponse{data=response.CreateTransactionResponse}
// @Failure      400 {object} response.ApiResponse "Stable errno in status_code: 10009 invalid params, 10041 invalid notify_url, 10004 invalid amount, 10014 chain disabled, 10016 unsupported asset, 10003 no wallet, 10005 no amount channel"
// @Router       /payments/gmpay/v1/order/create-transaction [post]
func (c *BaseCommController) CreateTransaction(ctx echo.Context) (err error) {
	req := new(request.CreateTransactionRequest)
	if err = ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err = c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	resp, err := service.CreateTransaction(req, apiKeyFromContext(ctx))
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, resp)
}

// SwitchNetwork 切换支付网络，补全占位父单或创建/返回子订单
// @Summary      Switch payment network
// @Description  Switch to a different payment target. A status=4 placeholder is completed in place and returns the same parent trade_id with is_selected=false; an already concrete status=1 parent creates or returns the only sub-order when switching to a different target.
// @Description  Normal values such as ton/tron/solana/ethereum select on-chain payment; the special value okpay selects OkPay hosted payment.
// @Description  For status=4 placeholders from GMPay or EPay submit.php, both on-chain targets and okpay complete the parent in place without creating a child order. Sub-orders cannot be switched again.
// @Description  For EPay orders with a merchant return_url, the returned redirect_url is the internal /pay/return/{trade_id} hop rather than the raw merchant return_url.
// @Tags         Payment
// @Accept       json
// @Produce      json
// @Param        request body request.SwitchNetworkRequest true "Switch network payload"
// @Success      200 {object} response.ApiResponse{data=response.CheckoutCounterResponse}
// @Failure      400 {object} response.ApiResponse "Stable errno in status_code: 10008 order not found, 10011 sub-order limit, 10012 cannot switch sub-order, 10013 not waiting payment, 10016 unsupported asset, 10017/10018/10019 provider errors, 10042 provider order creation failed"
// @Router       /pay/switch-network [post]
func (c *BaseCommController) SwitchNetwork(ctx echo.Context) (err error) {
	req := new(request.SwitchNetworkRequest)
	if err = ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err = c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	resp, err := service.SwitchNetwork(req)
	if err != nil {
		return c.FailJson(ctx, err)
	}

	jsonBytes, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return c.FailJson(ctx, constant.SystemErr)
	}

	log.Sugar.Debugf("switch network response: \n%s", string(jsonBytes))

	return c.SucJson(ctx, resp)
}

// CreateTransactionAndRedirect creates a transaction and redirects to
// the checkout counter. The route accepts BOTH GET (query string) and
// POST (form) per the legacy EPAY protocol; swagger documents POST as
// the canonical form — the GET variant is identical save the transport.
// @Summary      Create transaction and redirect (EPAY compat)
// @Description  Legacy EPAY-style endpoint. Accepts GET (querystring) and POST (form). On success, 302 redirects to /pay/checkout-counter/{trade_id}. Signature uses MD5 of sorted params + secret_key of the api_keys row matching the submitted pid. Optional request token/network/currency override database defaults and must be included in the EPay signature when sent. The server injects internal payment_type=Epay after EPay signature verification; merchants do not send GMPay payment_type to this endpoint.
// @Tags         Payment
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        pid query integer false "API key PID (GET query)"
// @Param        money query number false "Amount (fiat, GET query)"
// @Param        out_trade_no query string false "Merchant order ID (GET query)"
// @Param        notify_url query string false "Callback URL (GET query)"
// @Param        return_url query string false "Redirect URL after payment (GET query)"
// @Param        name query string false "Order name (GET query)"
// @Param        type query string false "Payment type (e.g. alipay, GET query)"
// @Param        sign query string false "MD5 signature (GET query)"
// @Param        sign_type query string false "Signature type (MD5, GET query)"
// @Param        pid formData integer true "API key PID"
// @Param        money formData number true "Amount (fiat)"
// @Param        out_trade_no formData string true "Merchant order ID"
// @Param        notify_url formData string true "Callback URL"
// @Param        return_url formData string false "Redirect URL after payment"
// @Param        name formData string false "Order name"
// @Param        type formData string false "Payment type (e.g. alipay)"
// @Param        sign formData string true "MD5 signature"
// @Param        sign_type formData string false "Signature type (MD5)"
// @Success      302 "Redirect to checkout counter"
// @Failure      400 {object} response.ApiResponse "Stable errno in status_code: 10009 invalid params, 10041 invalid notify_url, 10004 invalid amount, 10014 chain disabled, 10016 unsupported asset, 10003 no wallet, 10005 no amount channel"
// @Router       /payments/epay/v1/order/create-transaction/submit.php [post]
// @Router       /payments/epay/v1/order/create-transaction/submit.php [get]
func (c *BaseCommController) CreateTransactionAndRedirect(ctx echo.Context) (err error) {
	req := new(request.CreateTransactionRequest)
	if err = ctx.Bind(req); err != nil {
		log.Sugar.Errorf("bind request error: %v", err)
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err = c.ValidateStruct(ctx, req); err != nil {
		log.Sugar.Errorf("validate request error: %v", err)
		return c.FailJson(ctx, err)
	}
	resp, err := service.CreateTransaction(req, apiKeyFromContext(ctx))
	if err != nil {
		log.Sugar.Errorf("create transaction error: %v", err)
		return c.FailJson(ctx, err)
	}

	log.Sugar.Debugf("create transaction response: %+v", resp)

	tradeID := resp.TradeId
	return ctx.Redirect(http.StatusFound, "/pay/checkout-counter/"+tradeID)
}
