package response

// CreateTransactionResponse 创建订单成功返回
type CreateTransactionResponse struct {
	TradeId        string  `json:"trade_id"`        //  epusdt订单号
	OrderId        string  `json:"order_id"`        //  客户交易id
	Amount         float64 `json:"amount"`          //  订单金额，保留4位小数
	Currency       string  `json:"currency"`        //  订单货币类型 CNY USD......
	ActualAmount   float64 `json:"actual_amount"`   //  订单实际需要支付的金额，保留4位小数
	ReceiveAddress string  `json:"receive_address"` //  收款钱包地址
	Token          string  `json:"token"`           // 所属币种 TRX USDT......
	ExpirationTime int64   `json:"expiration_time"` // 过期时间 时间戳
	PaymentUrl     string  `json:"payment_url"`     // 收银台地址
}

// OrderNotifyResponse 订单异步回调结构体
type OrderNotifyResponse struct {
	TradeId            string  `json:"trade_id"`             //  epusdt订单号
	OrderId            string  `json:"order_id"`             //  客户交易id
	Amount             float64 `json:"amount"`               //  订单金额，保留4位小数
	ActualAmount       float64 `json:"actual_amount"`        //  订单实际需要支付的金额，保留4位小数
	ReceiveAddress     string  `json:"receive_address"`      //  收款钱包地址
	Token              string  `json:"token"`                // 所属币种 TRX USDT......
	Network            string  `json:"network"`              // 所属网络 TRON ETH ...
	BlockTransactionId string  `json:"block_transaction_id"` // 区块id
	Signature          string  `json:"signature"`            // 签名
	Status             int     `json:"status"`               //  1：等待支付，2：支付成功，3：已过期
}

// OrderNotifyResponseEpay epay订单异步回调结构体
type OrderNotifyResponseEpay struct {
	PID         int    `json:"pid"`          // 商户ID
	TradeNo     string `json:"trade_no"`     // 平台订单号
	OutTradeNo  string `json:"out_trade_no"` // 商户订单号
	Type        string `json:"type"`         // 订单类型
	Name        string `json:"name"`         // 商品名称
	Money       string `json:"money"`        // 订单金额，保留4位小数
	Sign        string `json:"sign"`         // 签名
	SignType    string `json:"sign_type"`    // 签名类型 // MD5
	TradeStatus string `json:"trade_status"` // 订单状态 // only has "TRADE_SUCCESS" for now
}
