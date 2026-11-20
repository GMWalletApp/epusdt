package response

// CreateTransactionResponse 创建订单成功返回
type CreateTransactionResponse struct {
	TradeId        string  `json:"trade_id" example:"3nQ9pL2xV7sK1mR8cT4yB_aZ"`                                     //  epusdt订单号
	OrderId        string  `json:"order_id" example:"ORD20260416001"`                                               //  客户交易id
	Amount         float64 `json:"amount" example:"100.0000"`                                                       //  订单金额，按 system.amount_precision 保留小数
	Currency       string  `json:"currency" example:"CNY"`                                                          //  订单货币类型 CNY USD......
	ActualAmount   float64 `json:"actual_amount" example:"14.2857"`                                                 //  订单实际需要支付的金额，按 system.amount_precision 保留小数
	ReceiveAddress string  `json:"receive_address" example:"TTestTronAddress001"`                                   //  收款钱包地址
	Token          string  `json:"token" example:"USDT"`                                                            // 所属币种 TRX USDT......
	Status         int     `json:"status" enums:"1,2,3,4" example:"1"`                                              // 订单状态 1=等待支付 2=支付成功 3=已过期 4=等待选择支付网络/币种
	ExpirationTime int64   `json:"expiration_time" example:"1713264600"`                                            // 过期时间 时间戳
	PaymentUrl     string  `json:"payment_url" example:"https://pay.example.com/checkout/3nQ9pL2xV7sK1mR8cT4yB_aZ"` // 收银台地址
}

// OrderNotifyResponse 订单异步回调结构体
type OrderNotifyResponse struct {
	Pid                string  `json:"pid" example:"1000"`                                                                   //  签名使用的商户 PID，商户据此查本地 secret 验签
	TradeId            string  `json:"trade_id" example:"3nQ9pL2xV7sK1mR8cT4yB_aZ"`                                          //  epusdt订单号
	OrderId            string  `json:"order_id" example:"ORD20260416001"`                                                    //  客户交易id
	Amount             float64 `json:"amount" example:"100.0000"`                                                            //  订单金额，按 system.amount_precision 保留小数
	ActualAmount       float64 `json:"actual_amount" example:"14.2857"`                                                      //  订单实际需要支付的金额，按 system.amount_precision 保留小数
	ReceiveAddress     string  `json:"receive_address" example:"TTestTronAddress001"`                                        //  收款钱包地址
	Token              string  `json:"token" example:"USDT"`                                                                 // 所属币种 TRX USDT......
	BlockTransactionId string  `json:"block_transaction_id" example:"0xabc123..."`                                           // 区块id
	Signature          string  `json:"signature" example:"498975a97bc34563bdb14df53fc18054645df9684d6c67d9b9dd90ec62be1018"` // GMPay 签名 HMAC-SHA256(secret_key, sorted_params)
	//  订单状态 1=等待支付 2=支付成功 3=已过期
	Status int `json:"status" enums:"1,2,3" example:"2"`
}

// OrderNotifyResponseEpay epay订单异步回调结构体
type OrderNotifyResponseEpay struct {
	PID         int    `json:"pid" example:"1001"`                          // 商户ID
	TradeNo     string `json:"trade_no" example:"3nQ9pL2xV7sK1mR8cT4yB_aZ"` // 平台订单号
	OutTradeNo  string `json:"out_trade_no" example:"ORD20260416001"`       // 商户订单号
	Type        string `json:"type" example:"usdt"`                         // 订单类型
	Name        string `json:"name" example:"VIP月卡"`                        // 商品名称
	Money       string `json:"money" example:"100.0000"`                    // 订单金额，保留4位小数
	Sign        string `json:"sign" example:"a1b2c3d4..."`                  // 签名
	SignType    string `json:"sign_type" example:"MD5"`                     // 签名类型
	TradeStatus string `json:"trade_status" example:"TRADE_SUCCESS"`        // 订单状态
}
