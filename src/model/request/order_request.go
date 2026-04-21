package request

import "github.com/gookit/validate"

// CreateTransactionRequest 创建交易请求
type CreateTransactionRequest struct {
	OrderId           string  `json:"order_id" validate:"required|maxLen:32"`
	Currency          string  `json:"currency" validate:"required"` // 法币 如：cny
	Token             string  `json:"token" validate:"required"`    // 币种 如：usdt
	Network           string  `json:"network" validate:"required"`  // 网络 如：TRON
	Amount            float64 `json:"amount" validate:"required|isFloat|min:0.01"`
	NotifyUrl         string  `json:"notify_url" validate:"required"`
	Signature         string  `json:"signature"  validate:"required"`
	RedirectUrl       string  `json:"redirect_url"`
	Name              string  `json:"name"`
	PaymentType       string  `json:"payment_type"`
	PaymentChannel    string  `json:"payment_channel"`
	PaymentMerchantId string  `json:"payment_merchant_id"`
}

func (r CreateTransactionRequest) Translates() map[string]string {
	return validate.MS{
		"OrderId":   "订单号",
		"Currency":  "货币",
		"Token":     "币种",
		"Network":   "网络",
		"Amount":    "支付金额",
		"NotifyUrl": "异步回调网址",
		"Signature": "签名",
	}
}

// OrderProcessingRequest 订单处理
type OrderProcessingRequest struct {
	ReceiveAddress     string
	Currency           string
	Token              string
	Network            string
	Amount             float64
	TradeId            string
	BlockTransactionId string
}

// SwitchNetworkRequest 切换支付网络
type SwitchNetworkRequest struct {
	TradeId string `json:"trade_id" validate:"required"`
	Token   string `json:"token" validate:"required"`
	Network string `json:"network" validate:"required"`
}

func (r SwitchNetworkRequest) Translates() map[string]string {
	return validate.MS{
		"TradeId": "订单号",
		"Token":   "币种",
		"Network": "网络",
	}
}
