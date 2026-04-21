package response

type CheckoutPaymentOption struct {
	Token   string `json:"token"`
	Network string `json:"network"`
}

type CheckoutCounterResponse struct {
	TradeId        string                  `json:"trade_id"`        //  epusdt订单号
	Amount         float64                 `json:"amount"`          //  订单金额，保留4位小数 法币金额
	ActualAmount   float64                 `json:"actual_amount"`   //  订单实际需要支付的金额，保留4位小数 加密货币金额
	Token          string                  `json:"token"`           //  所属币种 TRX USDT......
	Currency       string                  `json:"currency"`        //  法币币种 CNY USD ...
	ReceiveAddress string                  `json:"receive_address"` //  收款钱包地址
	Network        string                  `json:"network"`         //  网络 TRON ETH ...
	Status         int                     `json:"status"`          // 订单状态
	ExpirationTime int64                   `json:"expiration_time"` // 过期时间 时间戳
	RedirectUrl    string                  `json:"redirect_url"`
	CreatedAt      int64                   `json:"created_at"` // 订单创建时间 时间戳
	IsSelected     bool                    `json:"is_selected"`
	PaymentOptions []CheckoutPaymentOption `json:"payment_options,omitempty"`
}

type CheckStatusResponse struct {
	TradeId string `json:"trade_id"` //  epusdt订单号
	Status  int    `json:"status"`
}
