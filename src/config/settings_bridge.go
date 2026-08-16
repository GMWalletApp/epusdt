package config

import "time"

// Settings lookups are injected at runtime by bootstrap (after DB init)
// to keep the config package free of a `model/data` import, which would
// create a cycle via `model/dao -> config`.
//
// If unset (e.g. during early startup or tests) the getters return the
// zero string / 0 and callers apply their own fallback behavior.

// SettingsGetString is installed by bootstrap.Init with a closure that
// reads from the settings table. Runtime-only.
var SettingsGetString func(key string) string

// RateCacheSnapshot is the persistence-neutral representation used by the
// rate engine. Bootstrap wires these callbacks to the primary database while
// config tests can provide lightweight in-memory implementations.
type RateCacheSnapshot struct {
	Base          string             `json:"base"`
	Rates         map[string]float64 `json:"rates"`
	SourceURL     string             `json:"source_url"`
	LastSuccessAt *time.Time         `json:"last_success_at,omitempty"`
	LastAttemptAt *time.Time         `json:"last_attempt_at,omitempty"`
	LastRefreshOK bool               `json:"last_refresh_ok"`
	LastError     string             `json:"last_error"`
}

var (
	RateCacheLoad    func(base string) (RateCacheSnapshot, error)
	RateCacheLoadAll func() ([]RateCacheSnapshot, error)
	RateCacheSave    func(snapshot RateCacheSnapshot) error
)

func settingsRateApiUrl() string {
	if SettingsGetString == nil {
		return ""
	}
	return SettingsGetString("rate.api_url")
}

func settingsForcedRateList() string {
	if SettingsGetString == nil {
		return ""
	}
	return SettingsGetString("rate.forced_rate_list")
}

func settingsRateMode() string {
	if SettingsGetString == nil {
		return ""
	}
	return SettingsGetString("rate.mode")
}

func settingsRateCacheTTLSeconds() string {
	if SettingsGetString == nil {
		return ""
	}
	return SettingsGetString("rate.cache_ttl_seconds")
}
