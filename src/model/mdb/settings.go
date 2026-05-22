package mdb

// Setting stores non-credential runtime configuration as key/value pairs.
// Groups: brand, rate, system, epay, okpay. Merchant credentials (pid +
// secret_key) live in the api_keys table; notification configs live in
// NotificationChannel.
// Default rows for the epay/okpay groups are seeded on first startup (see
// model/dao/mdb_table_init.go seedDefaultSettings). All other groups start
// empty and fall back to hardcoded defaults until an admin sets them.
// Only exception: system.jwt_secret is auto-generated on first startup.
const (
	SettingGroupBrand  = "brand"
	SettingGroupRate   = "rate"
	SettingGroupSystem = "system"
	SettingGroupEpay   = "epay"
	SettingGroupOkPay  = "okpay"
)

const (
	SettingTypeString = "string"
	SettingTypeInt    = "int"
	SettingTypeBool   = "bool"
	SettingTypeJSON   = "json"
)

const (
	SettingKeyJwtSecret                = "system.jwt_secret"
	SettingKeyInitAdminPasswordPlain   = "system.init_admin_password_plain"
	SettingKeyInitAdminPasswordHash    = "system.init_admin_password_hash"
	SettingKeyInitAdminPasswordFetched = "system.init_admin_password_fetched"
	SettingKeyInitAdminPasswordChanged = "system.init_admin_password_changed"
	SettingKeyOrderExpiration          = "system.order_expiration_time"
	SettingKeyAmountPrecision          = "system.amount_precision"
	SettingKeyBrandCheckoutName        = "brand.checkout_name"
	SettingKeyBrandSiteName            = "brand.site_name"
	SettingKeyBrandLogoUrl             = "brand.logo_url"
	SettingKeyBrandSiteTitle           = "brand.site_title"
	SettingKeyBrandPageTitle           = "brand.page_title"
	SettingKeyBrandSuccessCopy         = "brand.success_copy"
	SettingKeyBrandPaySuccess          = "brand.pay_success_text"
	SettingKeyBrandSupportUrl          = "brand.support_url"
	SettingKeyBrandBackgroundColor     = "brand.background_color"
	SettingKeyBrandBackgroundImageUrl  = "brand.background_image_url"
	SettingKeyRateForcedRateList       = "rate.forced_rate_list"
	SettingKeyRateAdjustPercent        = "rate.adjust_percent"
	SettingKeyRateOkxC2cEnabled        = "rate.okx_c2c_enabled"
	SettingKeyRateApiUrl               = "rate.api_url"

	// EPAY route defaults — can be overridden via admin settings.
	SettingKeyEpayDefaultToken    = "epay.default_token"
	SettingKeyEpayDefaultCurrency = "epay.default_currency"
	SettingKeyEpayDefaultNetwork  = "epay.default_network"

	// OkPay hosted-checkout settings.
	SettingKeyOkPayEnabled        = "okpay.enabled"
	SettingKeyOkPayShopID         = "okpay.shop_id"
	SettingKeyOkPayShopToken      = "okpay.shop_token"
	SettingKeyOkPayAPIURL         = "okpay.api_url"
	SettingKeyOkPayCallbackURL    = "okpay.callback_url"
	SettingKeyOkPayReturnURL      = "okpay.return_url"
	SettingKeyOkPayTimeoutSeconds = "okpay.timeout_seconds"
	SettingKeyOkPayAllowTokens    = "okpay.allow_tokens"
)

type Setting struct {
	Group       string `gorm:"column:group;size:32;index:settings_group_index" json:"group" enums:"brand,rate,system,epay,okpay" example:"rate"`
	Key         string `gorm:"column:key;uniqueIndex:settings_key_uindex;size:128" json:"key" example:"rate.forced_rate_list"`
	Value       string `gorm:"column:value;type:text" json:"value" example:"{\"cny\":{\"usdt\":0.14635}}"`
	Type        string `gorm:"column:type;size:16;default:string" json:"type" enums:"string,int,bool,json" example:"json"`
	Description string `gorm:"column:description;size:255" json:"description" example:"强制汇率列表"`
	BaseModel
}

func (s *Setting) TableName() string {
	return "settings"
}
