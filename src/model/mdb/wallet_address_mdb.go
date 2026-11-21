package mdb

const (
	TokenStatusEnable  = 1
	TokenStatusDisable = 2
)

const (
	NetworkTron      = "tron"
	NetworkSolana    = "solana"
	NetworkEthereum  = "ethereum"
	NetworkBsc       = "binance"
	NetworkPolygon   = "polygon"
	NetworkPlasma    = "plasma"
	NetworkTon       = "ton"
	NetworkAptos     = "aptos"
	NetworkSui       = "sui"
	NetworkArbitrum  = "arbitrum"
	NetworkHyperEVM  = "hyperevm"
	NetworkMonad     = "monad"
	NetworkArc       = "arc"
	NetworkAvalanche = "avalanche"
	NetworkOptimism  = "optimism"
	NetworkBase      = "base"
	// NetworkXLayer is the hosted-checkout pseudo-network (OKX Onchain OS
	// Payment). There is no on-chain scanner for it: the chains/chain_tokens
	// rows exist so the admin chains/addresses pages can manage it like any
	// other network, and the wallet row is the single source of the
	// receiving address. See XLAYER-ADMIN-INTEGRATION-DESIGN.md.
	NetworkXLayer = "xlayer"
)

const (
	WalletSourceManual = "manual"
	WalletSourceImport = "import"
)

type WalletAddress struct {
	Network string `gorm:"column:network;uniqueIndex:wallet_address_network_address_uindex" json:"network" example:"tron"`
	Address string `gorm:"column:address;uniqueIndex:wallet_address_network_address_uindex" json:"address" example:"TTestTronAddress001"`
	// 状态 1=启用 2=禁用
	Status int64  `gorm:"column:status;default:1" json:"status" enums:"1,2" example:"1"`
	Remark string `gorm:"column:remark;size:255" json:"remark" example:"主钱包"`
	Source string `gorm:"column:source;size:16;default:manual" json:"source" enums:"manual,import" example:"manual"`
	BaseModel
}

func (w *WalletAddress) TableName() string {
	return "wallet_address"
}
