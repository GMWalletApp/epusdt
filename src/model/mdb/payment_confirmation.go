package mdb

// PaymentConfirmation survives feed acknowledgement and process restarts.
type PaymentConfirmation struct {
	ID       uint64 `gorm:"primaryKey"`
	OrderID  uint64 `gorm:"uniqueIndex:idx_candidate_tx;not null"`
	Hash     string `gorm:"size:128;uniqueIndex:idx_candidate_tx;not null"`
	Position string `gorm:"size:64;uniqueIndex:idx_candidate_tx;not null"`
	Activity string `gorm:"type:longtext;not null"`
	RetryAt  int64  `gorm:"index;not null"`
}
