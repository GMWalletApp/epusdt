package mdb

import "time"

// RateCache stores the last successful external rate payload for one base
// currency. Failed refreshes update only the attempt/result fields so the
// last usable rates survive API outages and process restarts.
type RateCache struct {
	Base          string     `gorm:"column:base;primaryKey;size:16" json:"base"`
	Rates         string     `gorm:"column:rates;type:text" json:"rates"`
	SourceURL     string     `gorm:"column:source_url;type:text" json:"source_url"`
	LastSuccessAt *time.Time `gorm:"column:last_success_at" json:"last_success_at,omitempty"`
	LastAttemptAt *time.Time `gorm:"column:last_attempt_at" json:"last_attempt_at,omitempty"`
	LastRefreshOK bool       `gorm:"column:last_refresh_ok;not null;default:false" json:"last_refresh_ok"`
	LastError     string     `gorm:"column:last_error;type:text" json:"last_error"`
	CreatedAt     time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (r *RateCache) TableName() string {
	return "rate_cache"
}
