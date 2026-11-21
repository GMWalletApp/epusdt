package mdb

const (
	ProviderOrderStatusCreating = "creating"
	ProviderOrderStatusPending  = "pending"
	ProviderOrderStatusPaid     = "paid"
	ProviderOrderStatusFailed   = "failed"
	ProviderOrderStatusExpired  = "expired"
)

// ProviderOrder stores provider-specific checkout data for one internal order
// row. For OkPay this can bind either to a status=4 parent completed in place
// or to a concrete child order created by later switch-network calls.
//
// Binding rules:
//   - trade_id always points to orders.trade_id
//   - the bound orders row should use the same pay_provider value
//   - provider identifies which downstream checkout created the provider row
//   - provider_order_id is the provider's own order number (for OkPay this is
//     the returned order_id from /payLink)
//   - pay_url is the hosted payment URL returned by the provider
//
// The main orders table remains the source of truth for merchant-facing state,
// while this table keeps provider-specific identifiers and callback payloads.
type ProviderOrder struct {
	// ReceiveAddress pins the destination used to create a hosted OKX order.
	ReceiveAddress string `gorm:"column:receive_address;size:64" json:"-"`
	TradeId        string `gorm:"column:trade_id;size:32;not null;uniqueIndex:provider_orders_trade_id_provider_uindex,priority:1;index:provider_orders_trade_id_index" json:"trade_id" example:"3nQ9pL2xV7sK1mR8cT4yB_aZ"`
	// Provider values should match the bound Orders.PayProvider value.
	Provider string `gorm:"column:provider;size:32;not null;uniqueIndex:provider_orders_trade_id_provider_uindex,priority:2;index:provider_orders_provider_status_index,priority:1" json:"provider" example:"okpay"`
	// ProviderOrderID is the downstream provider's own order identifier for the
	// bound concrete order row.
	ProviderOrderID string `gorm:"column:provider_order_id;size:128;not null;default:'';index:provider_orders_provider_order_id_index" json:"provider_order_id" example:"ac7b86615fdb137576ae35879f7ed844"`
	// PayURL is the hosted checkout URL returned by the downstream provider for
	// the bound concrete order row.
	PayURL string `gorm:"column:pay_url;size:512;not null;default:''" json:"pay_url" example:"https://pay.example.com/checkout/abc123"`
	// Amount/Coin mirror the exact payload submitted to the provider, which may
	// be useful when validating callbacks and troubleshooting mismatches.
	Amount float64 `gorm:"column:amount" json:"amount" example:"14.2857"`
	Coin   string  `gorm:"column:coin;size:16;not null;default:''" json:"coin" example:"USDT"`
	Status string  `gorm:"column:status;size:32;not null;default:pending;index:provider_orders_provider_status_index,priority:2" json:"status" example:"pending"`
	// NotifyRaw stores the original provider callback payload for auditing and
	// replay/debug use. It is provider-specific and intentionally opaque here.
	NotifyRaw string `gorm:"column:notify_raw;type:text" json:"notify_raw"`
	BaseModel
}

func (p *ProviderOrder) TableName() string {
	return "provider_orders"
}
