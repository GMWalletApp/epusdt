package mdb

// EvmScanCursor stores the last confirmed block processed for an EVM network.
// It lives in the runtime SQLite database so backfill scanners can resume
// after restarts without replaying the whole chain.
type EvmScanCursor struct {
	Network   string `gorm:"column:network;uniqueIndex:evm_scan_cursor_network_uindex;size:32" json:"network" example:"binance"`
	LastBlock int64  `gorm:"column:last_block;default:0" json:"last_block" example:"12345678"`
	BaseModel
}

func (c *EvmScanCursor) TableName() string {
	return "evm_scan_cursor"
}
