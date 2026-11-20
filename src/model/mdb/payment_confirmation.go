package mdb

// PaymentConfirmation survives feed acknowledgement and process restarts.
type PaymentConfirmation struct {
	ID             uint64 `gorm:"primaryKey"`
	OrderID        uint64 `gorm:"uniqueIndex:idx_candidate_tx;not null"`
	Hash           string `gorm:"size:128;uniqueIndex:idx_candidate_tx;not null"`
	Position       string `gorm:"size:64;uniqueIndex:idx_candidate_tx;not null"`
	Activity       string `gorm:"type:longtext;not null"`
	Attempts       int    `gorm:"not null;default:0"`
	PreviousPair   string `gorm:"size:260"`
	VerifiedResult string `gorm:"type:longtext"`
	RetryAt        int64  `gorm:"index;not null"`
}
