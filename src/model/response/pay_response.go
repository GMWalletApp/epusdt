package response

type CheckoutCounterResponse struct {
	TradeId        string  `json:"trade_id" example:"3nQ9pL2xV7sK1mR8cT4yB_aZ"`                                     //  epusdt订单号
	Amount         float64 `json:"amount" example:"100.0000"`                                                       //  订单金额，按 system.amount_precision 保留小数 法币金额
	ActualAmount   float64 `json:"actual_amount" example:"14.2857"`                                                 //  订单实际需要支付的金额；status=4 占位订单返回 0
	Token          string  `json:"token" example:"USDT"`                                                            //  所属币种；status=4 占位订单为空
	Currency       string  `json:"currency" example:"CNY"`                                                          //  法币币种 CNY USD ...
	ReceiveAddress string  `json:"receive_address" example:"TTestTronAddress001"`                                   //  收款钱包地址；status=4 占位订单为空
	Network        string  `json:"network" example:"tron"`                                                          //  网络 TRON ETH；status=4 占位订单为空
	Status         int     `json:"status" enums:"1,2,3,4" example:"1"`                                              // 订单状态 1=等待支付 2=支付成功 3=已过期 4=等待选择支付网络/币种；status=4 时前端应引导选择链上 token/network 或 OkPay
	PaymentType    string  `json:"payment_type" enums:"gmpay,epay" example:"gmpay"`                                 // 支付接入类型；底层 Epay/Gmpay 转为小写 epay/gmpay 返回
	ExpirationTime int64   `json:"expiration_time" example:"1713264600"`                                            // 过期时间 时间戳
	RedirectUrl    string  `json:"redirect_url" example:"https://example.com/success"`                              // 非 EPay 时为商户原始回跳地址；EPay 时为内部中转地址 /pay/return/{trade_id}
	PaymentUrl     string  `json:"payment_url" example:"https://pay.example.com/checkout/3nQ9pL2xV7sK1mR8cT4yB_aZ"` // 支付链接；链上订单为空，OkPay 订单为第三方 payLink
	CreatedAt      int64   `json:"created_at" example:"1713264000"`                                                 // 订单创建时间 时间戳
	IsSelected     bool    `json:"is_selected" example:"false"`                                                     // 是否已选择当前支付方式；status=4 占位订单和刚补全的占位父单为 false
}

type CheckStatusResponse struct {
	TradeId string `json:"trade_id" example:"3nQ9pL2xV7sK1mR8cT4yB_aZ"` //  epusdt订单号
	// 订单状态 1=等待支付 2=支付成功 3=已过期 4=等待选择支付网络/币种
	Status int `json:"status" enums:"1,2,3,4" example:"1"`
}

type ManualPaymentResponse struct {
	TradeId            string `json:"trade_id" example:"3nQ9pL2xV7sK1mR8cT4yB_aZ"`      // epusdt订单号
	Status             int    `json:"status" enums:"1,2,3" example:"2"`                 // 订单状态 1=等待支付 2=支付成功 3=已过期
	BlockTransactionId string `json:"block_transaction_id" example:"0xabc123def456..."` // 已验证并入账的交易哈希
}
