package request

import "github.com/gookit/validate"

// CreateTransactionRequest 创建交易请求
type CreateTransactionRequest struct {
	OrderId     string  `json:"order_id" form:"order_id" validate:"required|maxLen:32" example:"ORD20260416001"`
	Currency    string  `json:"currency" form:"currency" validate:"required" example:"cny"` // 法币 如：cny
	Token       string  `json:"token" form:"token" example:"usdt"`                          // 币种 如：usdt、ton；可与 network 同时缺省创建占位订单
	Network     string  `json:"network" form:"network" example:"tron"`                      // 网络 如：ton、tron、aptos；可与 token 同时缺省创建占位订单
	Amount      float64 `json:"amount" form:"amount" validate:"required|isFloat|gt:0.01" example:"100.00"`
	NotifyUrl   string  `json:"notify_url" form:"notify_url" validate:"required" example:"https://example.com/notify"`
	Signature   string  `json:"signature" form:"signature" validate:"required" example:"6f874b1919d95081835e2809b620e354a5866f5a6dbb2e432d1627f1eb10059d"`
	RedirectUrl string  `json:"redirect_url" form:"redirect_url" example:"https://example.com/success"`
	Name        string  `json:"name" form:"name" example:"VIP月卡"`
	// PaymentType is a compatibility flag, not a gateway selector. Only
	// "Epay" (case-insensitive) switches callback format to legacy EPay;
	// empty or any other value is stored as "Gmpay" and uses GMPay JSON.
	// It is optional for GMPay, but must be included in the signature when sent.
	PaymentType string `json:"payment_type" form:"payment_type" example:"Epay"`
	EpayType    string `json:"-" form:"-"`
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

// ManualPaymentRequest 手动提交交易 hash 补单
type ManualPaymentRequest struct {
	BlockTransactionId string `json:"block_transaction_id" validate:"required" example:"0xabc123def456..."`
}

func (r ManualPaymentRequest) Translates() map[string]string {
	return validate.MS{
		"BlockTransactionId": "交易哈希",
	}
}

// SwitchNetworkRequest 切换支付网络
type SwitchNetworkRequest struct {
	TradeId string `json:"trade_id" validate:"required" example:"3nQ9pL2xV7sK1mR8cT4yB_aZ"`
	Token   string `json:"token" validate:"required" example:"USDT"`
	Network string `json:"network" validate:"required" example:"okpay,tron,solana,ethereum,aptos"`
}

func (r SwitchNetworkRequest) Translates() map[string]string {
	return validate.MS{
		"TradeId": "订单号",
		"Token":   "币种",
		"Network": "网络",
	}
}
