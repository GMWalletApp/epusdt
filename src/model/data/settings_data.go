package data

import (
	"strconv"
	"strings"
	"sync"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm/clause"
)

// Simple in-process cache. Settings are read frequently (on every order
// create, every scanner tick, etc.); the DB round-trip isn't free. Cache
// is invalidated explicitly via SetString/Delete and expires on process
// restart — that's enough for admin-edit semantics without TTL complexity.
var (
	settingsCache   = map[string]string{}
	settingsCacheMu sync.RWMutex
	settingsLoadMu  sync.Mutex // serializes the lazy bootstrap load
	settingsLoaded  bool
)

func loadAllSettings() error {
	var rows []mdb.Setting
	if err := dao.Mdb.Find(&rows).Error; err != nil {
		return err
	}
	settingsCacheMu.Lock()
	defer settingsCacheMu.Unlock()
	settingsCache = make(map[string]string, len(rows))
	for _, r := range rows {
		settingsCache[r.Key] = r.Value
	}
	settingsLoaded = true
	return nil
}

// ensureLoaded performs a thread-safe lazy bootstrap of the cache. Uses
// a dedicated mutex + double-checked flag so two concurrent callers
// don't both hit the DB. Can't use sync.Once because ReloadSettings
// needs to be able to rebuild after a failed initial load.
func ensureLoaded() {
	settingsCacheMu.RLock()
	loaded := settingsLoaded
	settingsCacheMu.RUnlock()
	if loaded {
		return
	}
	settingsLoadMu.Lock()
	defer settingsLoadMu.Unlock()
	// Recheck under the load mutex — another goroutine may have loaded
	// while we were blocked.
	settingsCacheMu.RLock()
	loaded = settingsLoaded
	settingsCacheMu.RUnlock()
	if loaded {
		return
	}
	_ = loadAllSettings()
}

// ReloadSettings forces a refresh from DB. Call after external writes.
func ReloadSettings() error {
	settingsLoadMu.Lock()
	defer settingsLoadMu.Unlock()
	return loadAllSettings()
}

// GetSettingString returns the string value for key, or fallback if unset.
func GetSettingString(key, fallback string) string {
	ensureLoaded()
	settingsCacheMu.RLock()
	defer settingsCacheMu.RUnlock()
	v, ok := settingsCache[key]
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// GetSettingInt returns the int value for key, or fallback on miss/parse fail.
func GetSettingInt(key string, fallback int) int {
	v := GetSettingString(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// GetSettingFloat returns the float value for key, or fallback on miss/parse fail.
func GetSettingFloat(key string, fallback float64) float64 {
	v := GetSettingString(key, "")
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

// GetSettingBool returns the bool value for key, or fallback on miss/parse fail.
func GetSettingBool(key string, fallback bool) bool {
	v := GetSettingString(key, "")
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

const (
	DefaultAmountPrecision = 2
	MinAmountPrecision     = 2
	MaxAmountPrecision     = 6
)

// NormalizeAmountPrecision clamps external precision values to the supported
// range used by order creation and transaction matching.
func NormalizeAmountPrecision(precision int) int {
	if precision < MinAmountPrecision || precision > MaxAmountPrecision {
		return DefaultAmountPrecision
	}
	return precision
}

func GetAmountPrecision() int {
	return NormalizeAmountPrecision(GetSettingInt(mdb.SettingKeyAmountPrecision, DefaultAmountPrecision))
}

// EnsureDefaultForcedRateList restores the built-in CNY stablecoin rate only
// when the forced rate setting is completely empty. Any non-empty user JSON is
// treated as intentional and is never merged or overwritten.
func EnsureDefaultForcedRateList() error {
	if strings.TrimSpace(GetSettingString(mdb.SettingKeyRateForcedRateList, "")) != "" {
		return nil
	}
	return SetSetting(mdb.SettingGroupRate, mdb.SettingKeyRateForcedRateList, mdb.SettingDefaultRateForcedRateList, mdb.SettingTypeJSON)
}

// SetSetting upserts a setting row and refreshes the cache entry.
func SetSetting(group, key, value, valueType string) error {
	if valueType == "" {
		valueType = mdb.SettingTypeString
	}
	row := mdb.Setting{Group: group, Key: key, Value: value, Type: valueType}
	updates := clause.AssignmentColumns([]string{"group", "value", "type", "updated_at"})
	updates = append(updates, clause.Assignment{Column: clause.Column{Name: "deleted_at"}, Value: nil})
	err := dao.Mdb.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: updates,
	}).Create(&row).Error
	if err != nil {
		return err
	}
	settingsCacheMu.Lock()
	settingsCache[key] = value
	settingsCacheMu.Unlock()
	return nil
}

// DeleteSetting removes a setting row and drops the cache entry.
func DeleteSetting(key string) error {
	if err := dao.Mdb.Where("key = ?", key).Delete(&mdb.Setting{}).Error; err != nil {
		return err
	}
	settingsCacheMu.Lock()
	delete(settingsCache, key)
	settingsCacheMu.Unlock()
	return nil
}

// sensitiveSettingKeys lists keys that must never be returned to API callers.
var sensitiveSettingKeys = []string{
	mdb.SettingKeyJwtSecret,
	mdb.SettingKeyInitAdminPasswordPlain,
	mdb.SettingKeyInitAdminPasswordHash,
	mdb.SettingKeyInitAdminPasswordFetched,
	mdb.SettingKeyInitAdminPasswordChanged,
}

func getFirstNonEmptySetting(fallback string, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(GetSettingString(key, ""))
		if value != "" {
			return value
		}
	}
	return fallback
}

func GetBrandCashierName() string {
	return getFirstNonEmptySetting("", mdb.SettingKeyBrandCheckoutName, mdb.SettingKeyBrandSiteName)
}

func GetBrandLogoURL() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyBrandLogoUrl, ""))
}

func GetBrandWebsiteTitle() string {
	return getFirstNonEmptySetting("", mdb.SettingKeyBrandSiteTitle, mdb.SettingKeyBrandPageTitle)
}

func GetBrandSupportURL() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyBrandSupportUrl, ""))
}

func GetBrandBackgroundColor() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyBrandBackgroundColor, ""))
}

func GetBrandBackgroundImageURL() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyBrandBackgroundImageUrl, ""))
}

// OkPay settings helpers keep the provider-specific defaults in one place so
// business logic does not need to repeat raw settings keys.
func GetOkPayEnabled() bool {
	return GetSettingBool(mdb.SettingKeyOkPayEnabled, false)
}

func GetOkPayShopID() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyOkPayShopID, ""))
}

func GetOkPayShopToken() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyOkPayShopToken, ""))
}

func GetOkPayAPIURL() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyOkPayAPIURL, "https://api.okaypay.me/shop/"))
}

func GetOkPayCallbackURL() string {
	if configured := strings.TrimSpace(GetSettingString(mdb.SettingKeyOkPayCallbackURL, "")); configured != "" {
		return configured
	}
	appURI := strings.TrimSpace(config.GetAppUri())
	if appURI == "" {
		return ""
	}
	return strings.TrimRight(appURI, "/") + "/payments/okpay/v1/notify"
}

func GetOkPayReturnURL() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyOkPayReturnURL, ""))
}

func GetOkPayTimeoutSeconds() int {
	return GetSettingInt(mdb.SettingKeyOkPayTimeoutSeconds, 10)
}

func GetOkPayAllowTokens() []string {
	raw := GetSettingString(mdb.SettingKeyOkPayAllowTokens, "USDT,TRX")
	if strings.TrimSpace(raw) == "" {
		return []string{"USDT", "TRX"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		token := strings.ToUpper(strings.TrimSpace(part))
		if token == "" {
			continue
		}
		out = append(out, token)
	}
	if len(out) == 0 {
		return []string{"USDT", "TRX"}
	}
	return out
}

// X Layer settings helpers (OKX Onchain OS Payment hosted checkout).
// Token symbols are normalized to upper-case ASCII: the doc's USD₮0 is
// exposed as USDT0.
func GetXLayerEnabled() bool {
	return GetSettingBool(mdb.SettingKeyXLayerEnabled, false)
}

func GetXLayerAPIKey() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyXLayerAPIKey, ""))
}

func GetXLayerAPISecret() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyXLayerAPISecret, ""))
}

func GetXLayerPassphrase() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyXLayerPassphrase, ""))
}

// GetXLayerAPIURL was removed (2026-11-21 4th review): the Onchain OS
// Payment base URL is the fixed xlayerAPIBaseURL constant in
// model/service/xlayer_service.go; the legacy xlayer.api_url settings
// key is no longer read.

func GetXLayerReturnURL() string {
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyXLayerReturnURL, ""))
}

func GetXLayerTimeoutSeconds() int {
	return GetSettingInt(mdb.SettingKeyXLayerTimeoutSeconds, 10)
}

// GetXLayerToAddress returns the merchant X Layer receiving address. The
// wallet_address table (network=xlayer, status=enabled) is the single source
// of truth since the admin-integration change — the address is managed on
// the admin addresses page like any other network. With multiple enabled
// rows the oldest one wins: the hosted checkout takes exactly one `to`.
// Falls back to the legacy settings key when no enabled wallet row exists
// (installs seeded before the migration, and unit tests). See
// XLAYER-ADMIN-INTEGRATION-DESIGN.md.
func GetXLayerToAddress() string {
	var row mdb.WalletAddress
	err := dao.Mdb.Model(&mdb.WalletAddress{}).
		Where("network = ? AND status = ?", mdb.NetworkXLayer, mdb.TokenStatusEnable).
		Order("id ASC").Limit(1).Find(&row).Error
	if err == nil && row.ID > 0 {
		return strings.TrimSpace(row.Address)
	}
	return strings.TrimSpace(GetSettingString(mdb.SettingKeyXLayerToAddress, ""))
}

// GetXLayerAllowTokens returns the X Layer tokens the cashier may offer.
// The chain_tokens table (network=xlayer) is the single source of truth —
// the per-token switches on the admin chains page write there — so the
// cashier listing and the switch-network validation (xlayerTokenAllowed)
// always agree. When the table carries no xlayer rows at all (unit tests,
// un-migrated installs), the legacy settings key is used as a fallback.
// An all-disabled table intentionally yields an empty list.
func GetXLayerAllowTokens() []string {
	var rows []mdb.ChainToken
	err := dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ?", mdb.NetworkXLayer).
		Order("id ASC").Find(&rows).Error
	if err == nil && len(rows) > 0 {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			if !row.Enabled {
				continue
			}
			token := strings.ToUpper(strings.TrimSpace(row.Symbol))
			if token == "" {
				continue
			}
			out = append(out, token)
		}
		return out
	}
	raw := GetSettingString(mdb.SettingKeyXLayerAllowTokens, "USDC,USDT0,USDG")
	if strings.TrimSpace(raw) == "" {
		return []string{"USDC", "USDT0", "USDG"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		token := strings.ToUpper(strings.TrimSpace(part))
		if token == "" {
			continue
		}
		out = append(out, token)
	}
	if len(out) == 0 {
		return []string{"USDC", "USDT0", "USDG"}
	}
	return out
}

// ListSettingsByGroup returns all rows for a given group (empty group = all),
// excluding any keys in sensitiveSettingKeys.
func ListSettingsByGroup(group string) ([]mdb.Setting, error) {
	var rows []mdb.Setting
	tx := dao.Mdb.Model(&mdb.Setting{}).Not("`key`", sensitiveSettingKeys)
	if group != "" {
		tx = tx.Where("`group` = ?", group)
	}
	err := tx.Order("`key` ASC").Find(&rows).Error
	return rows, err
}
