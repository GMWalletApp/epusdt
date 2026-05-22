package config

// Settings lookups are injected at runtime by bootstrap (after DB init)
// to keep the config package free of a `model/data` import, which would
// create a cycle via `model/dao -> config`.
//
// If unset (e.g. during early startup or tests) the getters return the
// zero string / 0 and callers apply their own fallback behavior.

// SettingsGetString is installed by bootstrap.Init with a closure that
// reads from the settings table. Runtime-only.
var SettingsGetString func(key string) string

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
