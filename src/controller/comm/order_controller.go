package comm

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/GMWalletApp/epusdt/middleware"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/sign"
	"github.com/labstack/echo/v4"
)

const EPayTypeContextKey = "epay_type"

// apiKeyFromContext 返回验签流程写入上下文的 API Key。
// 受保护路由正常执行时该值不应为空。
func apiKeyFromContext(ctx echo.Context) *mdb.ApiKey {
	if v, ok := ctx.Get(middleware.ApiKeyRowKey).(*mdb.ApiKey); ok {
		return v
	}
	return nil
}

// gmpaySignAlgorithmFromContext 读取中间件实际验签成功的算法。
func gmpaySignAlgorithmFromContext(ctx echo.Context) (string, bool) {
	algorithm, ok := ctx.Get(middleware.SignAlgorithmKey).(string)
	if !ok || sign.NormalizeAlgorithm(algorithm) == "" {
		return "", false
	}
	return algorithm, true
}

// CreateTransaction 创建交易
// @Summary      Create transaction
// @Description  Create a payment transaction order. Accepts JSON body (application/json) or form-encoded body (application/x-www-form-urlencoded).
// @Description  GMPay may omit both token and network to create a status=4 placeholder order; EPay submit.php can also create one when neither request parameters nor database defaults provide token/network. Supplying only one of token/network is invalid.
// @Description  GMPay 认证必须提供 pid 与 signature。原始请求中除 signature 外的所有非空字符串或数字字段都会参与签名；未知字段不会写入订单，但客户端仍必须把它们计入签名。
// @Description  payment_type 可省略；非空时参与 GMPay 签名。即使值为 Epay，也只切换回调格式，不会把本接口的入站验签切换为 EPay MD5。
// @Description  GMPay 签名算法由 API Key 的 gmpay_sign_mode 控制；新建 Key 默认 HMAC-SHA256，升级前已有 Key 在升级后默认 dual 以兼容旧 MD5。该设置不影响独立的 EPay 接口。
// @Description  network 使用公开配置返回的真实标识，例如 tron、ethereum、binance、base；BSC 的接口标识是 binance，不是 bsc。
// @Tags         Payment
// @Accept       json
// @Accept       x-www-form-urlencoded
// @Produce      json
// @Param        request body request.GMPayCreateTransactionDocRequest true "GMPay 创建订单 JSON 请求体"
// @Param        pid formData string true "API Key 的 PID"
// @Param        order_id formData string true "商户订单号"
// @Param        currency formData string true "法币币种，例如 cny"
// @Param        token formData string false "Crypto token (e.g. TON, USDT); omit together with network to create a placeholder where supported"
// @Param        network formData string false "网络标识，例如 tron、binance；支持时可与 token 同时省略以创建占位订单"
// @Param        amount formData number true "法币金额"
// @Param        notify_url formData string true "异步回调地址"
// @Param        signature formData string true "GMPay 签名：64 位 HMAC-SHA256，兼容模式下也可使用 32 位 MD5"
// @Param        redirect_url formData string false "Redirect URL"
// @Param        name formData string false "Order name"
// @Param        payment_type formData string false "Optional GMPay compatibility flag; include in signature when sent"
// @Success      200 {object} response.ApiResponse{data=response.CreateTransactionResponse}
// @Failure      400 {object} response.ApiResponse "Stable errno in status_code: 10009 invalid params, 10041 invalid notify_url, 10004 invalid amount, 10014 chain disabled, 10016 unsupported asset, 10003 no wallet, 10005 no amount channel"
// @Failure      401 {object} response.ApiResponse "pid/signature 缺失、API Key 不可用、IP 不在白名单或签名错误"
// @Router       /payments/gmpay/v1/order/create-transaction [post]
func (c *BaseCommController) CreateTransaction(ctx echo.Context) (err error) {
	req := new(request.CreateTransactionRequest)
	if err = ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err = c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	algorithm, ok := gmpaySignAlgorithmFromContext(ctx)
	if !ok {
		return c.FailJson(ctx, constant.SignatureErr)
	}
	resp, err := service.CreateTransactionWithSignAlgorithm(req, apiKeyFromContext(ctx), algorithm)
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

// CreateTransactionAndRedirect 创建 EPay 兼容订单并跳转到收银台。
// 路由同时接受 GET 查询参数和 POST 表单，二者采用相同的参数及验签规则。
// @Summary      Create transaction and redirect (EPAY compat)
// @Description  Legacy EPAY-style endpoint. Accepts GET (querystring) and POST (form). On success, 302 redirects to /pay/checkout-counter/{trade_id}. Signature uses MD5 of sorted params + secret_key of the api_keys row matching the submitted pid.
// @Description  After signature verification, type accepts only either alipay or a supported type=token.network selector (for example usdt.tron). Token/network resolution is: supported selector first; otherwise request token/network; otherwise epay.default_token / epay.default_network. If token and network are still both empty, the order is created as status=4 placeholder. Supplying only one of token/network remains invalid.
// @Description  Currency resolution is unchanged: request currency -> epay.default_currency -> cny. Supported type selectors bypass only token/network defaults, not currency fallback.
// @Description  Success return/notify reuse the stored request type. On this branch that means either alipay or a supported token.network selector; when the request omitted type, outbound fallback remains alipay. The server injects internal payment_type=Epay after EPay signature verification; merchants do not send GMPay payment_type to this endpoint.
// @Description  EPay 认证必须提供 pid 与 sign，并始终使用 MD5；sign_type 不参与签名。API Key 的 gmpay_sign_mode 只作用于 GMPay，不改变本接口算法。
// @Description  除 sign、sign_type 外，原始请求中的所有非空参数都会参与 EPay 签名；未知参数不会写入订单，但仍必须计入签名。
// @Description  network 使用真实标识，例如 tron、ethereum、binance、base；BSC 的接口标识是 binance，不是 bsc。
// @Tags         Payment
// @Accept       x-www-form-urlencoded
// @Produce      html
// @Param        pid query string true "API Key 的 PID（GET 查询参数）"
// @Param        money query number true "法币金额（GET 查询参数）"
// @Param        out_trade_no query string true "商户订单号（GET 查询参数）"
// @Param        notify_url query string true "异步回调地址（GET 查询参数）"
// @Param        return_url query string false "Redirect URL after payment (GET query)"
// @Param        name query string false "Order name (GET query)"
// @Param        type query string false "Either alipay or a supported token.network selector such as usdt.tron (GET query)"
// @Param        token query string false "type 未命中选择器时使用的币种（GET 查询参数）"
// @Param        network query string false "type 未命中选择器时使用的网络，例如 tron、binance（GET 查询参数）"
// @Param        currency query string false "法币币种（GET 查询参数）"
// @Param        sign query string true "MD5 签名（GET 查询参数）"
// @Param        sign_type query string false "Signature type (MD5, GET query)"
// @Param        pid formData string true "API Key 的 PID"
// @Param        money formData number true "法币金额"
// @Param        out_trade_no formData string true "商户订单号"
// @Param        notify_url formData string true "异步回调地址"
// @Param        return_url formData string false "Redirect URL after payment"
// @Param        name formData string false "Order name"
// @Param        type formData string false "Either alipay or a supported token.network selector such as usdt.tron"
// @Param        token formData string false "type 未命中选择器时使用的币种"
// @Param        network formData string false "type 未命中选择器时使用的网络，例如 tron、binance"
// @Param        currency formData string false "法币币种"
// @Param        sign formData string true "MD5 签名"
// @Param        sign_type formData string false "Signature type (MD5)"
// @Success      302 "Redirect to checkout counter"
// @Failure      400 {object} response.ApiResponse "Stable errno in status_code: 10009 invalid params, 10041 invalid notify_url, 10004 invalid amount, 10014 chain disabled, 10016 unsupported asset, 10003 no wallet, 10005 no amount channel"
// @Failure      401 {object} response.ApiResponse "pid/sign 缺失、API Key 不可用、IP 不在白名单或签名错误"
// @Router       /payments/epay/v1/order/create-transaction/submit.php [post]
// @Router       /payments/epay/v1/order/create-transaction/submit.php [get]
func (c *BaseCommController) CreateTransactionAndRedirect(ctx echo.Context) (err error) {
	req := new(request.CreateTransactionRequest)
	if err = ctx.Bind(req); err != nil {
		log.Sugar.Errorf("bind request error: %v", err)
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if raw, ok := ctx.Get(EPayTypeContextKey).(string); ok {
		req.EpayType = strings.TrimSpace(raw)
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
