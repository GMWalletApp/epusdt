package admin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/telegram"
	"github.com/labstack/echo/v4"
)

// SettingUpsertItem is a single setting entry for batch upsert.
// Supported groups and keys:
//
//   - group=rate:
//     rate.forced_rate_list  (json)   — override rate map, e.g. {"cny":{"usdt":0.14635}}; base/coin keys are normalized to lowercase
//     rate.api_url           (string) — external rate API URL used when no positive forced rate exists
//     rate.adjust_percent    (float)  — rate adjustment percentage
//     rate.okx_c2c_enabled   (bool)   — use OKX C2C rate feed
//
//   - group=epay:
//     epay.default_token     (string) — token for EPAY orders, e.g. "usdt" (default)
//     epay.default_currency  (string) — fiat currency for EPAY orders, e.g. "cny" (default)
//     epay.default_network   (string) — blockchain network for EPAY orders, e.g. "tron" (default)
//
//   - group=okpay:
//     okpay.enabled          (bool)   — enable OkPay as a switch-network payment option
//     okpay.shop_id          (string) — OkPay merchant/shop identifier
//     okpay.shop_token       (string) — OkPay signing token
//     okpay.api_url          (string) — OkPay API base URL
//     okpay.callback_url     (string) — server callback URL used for OkPay notify
//     okpay.return_url       (string) — optional default browser return URL after payment
//     okpay.timeout_seconds  (int)    — outbound OkPay API timeout in seconds
//     okpay.allow_tokens     (string) — comma-separated allowed tokens, e.g. "USDT,TRX"
//
//   - group=brand:
//     brand.checkout_name    (string) — cashier display name (preferred)
//     brand.logo_url         (string) — logo image URL
//     brand.site_title       (string) — payment page title / website title (preferred)
//     brand.success_copy     (string) — text shown on payment success (preferred)
//     brand.support_url      (string) — support / help URL
//     brand.background_color (string) — checkout background color
//     brand.background_image_url (string) — checkout background image URL
//     brand.site_name        (string) — legacy cashier/site display name
//     brand.page_title       (string) — legacy payment page title
//     brand.pay_success_text (string) — legacy success text
//
//   - group=system:
//     system.order_expiration_time (int) — order expiry in minutes
//     system.amount_precision      (int) — payment amount precision, 2-6 decimals (default 2)
type SettingUpsertItem struct {
	Group string      `json:"group" enums:"brand,rate,system,epay,okpay" example:"epay"`
	Key   string      `json:"key" example:"epay.default_network"`
	Value interface{} `json:"value"`
	Type  string      `json:"type" enums:"string,int,bool,json" example:"string"`
}

// SettingsUpsertRequest is the payload for batch upserting settings.
type SettingsUpsertRequest struct {
	Items []SettingUpsertItem `json:"items" validate:"required"`
}

// ListSettings returns all rows, optionally filtered by group.
// @Summary      List settings
// @Description  Returns all settings, optionally filtered by group.
// @Description  Available groups: brand, rate, system, epay, okpay.
// @Description  See SettingUpsertItem for the full list of supported keys per group.
// @Tags         Admin Settings
// @Security     AdminJWT
// @Produce      json
// @Param        group query string false "Group filter (brand|rate|system|epay|okpay)"
// @Success      200 {object} response.ApiResponse{data=[]mdb.Setting}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/settings [get]
func (c *BaseAdminController) ListSettings(ctx echo.Context) error {
	group := strings.ToLower(strings.TrimSpace(ctx.QueryParam("group")))
	rows, err := data.ListSettingsByGroup(group)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, rows)
}

// UpsertSettings batch-inserts / updates rows. Each item is treated
// independently so a malformed row in the middle doesn't drop earlier
// ones. Errors are returned per-key so the UI can surface them.
// @Summary      Upsert settings
// @Description  Batch insert/update settings. Returns per-key status.
// @Description  Supported groups: brand, rate, system, epay, okpay.
// @Description  epay group keys: epay.default_token (e.g. "usdt"), epay.default_currency (e.g. "cny"), epay.default_network (e.g. "tron").
// @Description  okpay group keys: okpay.enabled, okpay.shop_id, okpay.shop_token, okpay.api_url, okpay.callback_url, okpay.return_url, okpay.timeout_seconds, okpay.allow_tokens.
// @Description  rate group keys: rate.forced_rate_list (JSON map, e.g. {"cny":{"usdt":0.14635}}; base/coin keys are normalized to lowercase), rate.api_url, rate.adjust_percent, rate.okx_c2c_enabled.
// @Description  brand group keys: brand.checkout_name, brand.logo_url, brand.site_title, brand.success_copy, brand.support_url, brand.background_color, brand.background_image_url. Legacy aliases brand.site_name, brand.page_title and brand.pay_success_text are also supported.
// @Description  system group keys: system.order_expiration_time, system.amount_precision (int, 2-6, default 2).
// @Tags         Admin Settings
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        request body admin.SettingsUpsertRequest true "Settings payload"
// @Success      200 {object} response.ApiResponse{data=[]admin.SettingsUpsertResult}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/settings [put]
func (c *BaseAdminController) UpsertSettings(ctx echo.Context) error {
	req := new(SettingsUpsertRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, err)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	type result struct {
		Key   string `json:"key"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	out := make([]result, 0, len(req.Items))
	for _, item := range req.Items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			out = append(out, result{Key: item.Key, OK: false, Error: "key required"})
			continue
		}
		value, err := normalizeSettingValue(item.Value)
		if err != nil {
			out = append(out, result{Key: key, OK: false, Error: err.Error()})
			continue
		}
		value, err = normalizeAndValidateSettingItem(item.Group, key, value)
		if err != nil {
			out = append(out, result{Key: key, OK: false, Error: err.Error()})
			continue
		}
		if key == mdb.SettingKeyAmountPrecision {
			item.Group = mdb.SettingGroupSystem
			item.Type = mdb.SettingTypeInt
		}
		if key == mdb.SettingKeyRateForcedRateList {
			item.Group = mdb.SettingGroupRate
			item.Type = mdb.SettingTypeJSON
		}
		if err := data.SetSetting(item.Group, key, value, item.Type); err != nil {
			out = append(out, result{Key: key, OK: false, Error: err.Error()})
			continue
		}
		out = append(out, result{Key: key, OK: true})
	}

	// When telegram credentials are updated via settings, reload the
	// command bot so operators don't need to restart the process, and
	// sync the notification_channels row so the notify dispatcher picks
	// up the new values immediately.
	telegramKeys := map[string]bool{
		"system.telegram_bot_token":               true,
		"system.telegram_chat_id":                 true,
		"system.telegram_payment_notice_enabled":  true,
		"system.telegram_abnormal_notice_enabled": true,
	}
	for _, item := range req.Items {
		if telegramKeys[strings.TrimSpace(item.Key)] {
			telegram.ReloadBotAsync("settings upsert")
			go dao.SyncTelegramChannelFromSettings()
			break
		}
	}

	return c.SucJson(ctx, out)
}

func normalizeSettingValue(value interface{}) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("value must be JSON serializable")
		}
		return string(b), nil
	}
}

func normalizeAndValidateSettingItem(group, key, value string) (string, error) {
	switch key {
	case mdb.SettingKeyAmountPrecision:
		if strings.ToLower(strings.TrimSpace(group)) != mdb.SettingGroupSystem {
			return value, fmt.Errorf("%s must use group %s", key, mdb.SettingGroupSystem)
		}
		precision, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return value, fmt.Errorf("%s must be an integer", key)
		}
		if precision < data.MinAmountPrecision || precision > data.MaxAmountPrecision {
			return value, fmt.Errorf("%s must be between %d and %d", key, data.MinAmountPrecision, data.MaxAmountPrecision)
		}
	case mdb.SettingKeyRateForcedRateList:
		normalized, err := normalizeForcedRateListSetting(group, key, value)
		if err != nil {
			return value, err
		}
		return normalized, nil
	}
	return value, nil
}

func normalizeForcedRateListSetting(group, key, value string) (string, error) {
	if strings.ToLower(strings.TrimSpace(group)) != mdb.SettingGroupRate {
		return value, fmt.Errorf("%s must use group %s", key, mdb.SettingGroupRate)
	}
	if strings.TrimSpace(value) == "" {
		return "", nil
	}

	var rates map[string]map[string]float64
	if err := json.Unmarshal([]byte(value), &rates); err != nil {
		return value, fmt.Errorf("%s must be valid JSON object", key)
	}

	normalized := make(map[string]map[string]float64, len(rates))
	for base, coins := range rates {
		normalizedBase := strings.ToLower(strings.TrimSpace(base))
		if normalizedBase == "" {
			return value, fmt.Errorf("%s base currency must not be empty", key)
		}
		if coins == nil {
			return value, fmt.Errorf("%s.%s must be an object", key, base)
		}
		normalizedCoins, ok := normalized[normalizedBase]
		if !ok {
			normalizedCoins = make(map[string]float64, len(coins))
			normalized[normalizedBase] = normalizedCoins
		}
		for coin, rate := range coins {
			normalizedCoin := strings.ToLower(strings.TrimSpace(coin))
			if normalizedCoin == "" {
				return value, fmt.Errorf("%s coin must not be empty", key)
			}
			if rate < 0 {
				return value, fmt.Errorf("%s.%s.%s must be >= 0", key, base, coin)
			}
			if _, exists := normalizedCoins[normalizedCoin]; exists {
				return value, fmt.Errorf("%s.%s.%s is duplicated after normalization", key, normalizedBase, normalizedCoin)
			}
			normalizedCoins[normalizedCoin] = rate
		}
	}

	normalizedValue, err := json.Marshal(normalized)
	if err != nil {
		return value, fmt.Errorf("%s must be JSON serializable", key)
	}
	return string(normalizedValue), nil
}

// DeleteSetting removes one row. The next read of that key will fall
// back to the hardcoded default (see settings_data.GetSetting*).
// @Summary      Delete setting
// @Description  Remove a setting by key (falls back to default)
// @Tags         Admin Settings
// @Security     AdminJWT
// @Produce      json
// @Param        key path string true "Setting key"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/settings/{key} [delete]
func (c *BaseAdminController) DeleteSetting(ctx echo.Context) error {
	key := strings.TrimSpace(ctx.Param("key"))
	if key == "" {
		return c.SucJson(ctx, nil)
	}
	if err := data.DeleteSetting(key); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}
