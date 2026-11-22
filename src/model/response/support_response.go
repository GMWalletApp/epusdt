package response

// TokenDetails identifies the on-chain asset. Native assets without an identifier use an empty address.
type TokenDetails struct {
	ContractAddress string `json:"contract_address"`
	Decimals        int    `json:"decimals"`
}

type NetworkTokenSupport struct {
	Network      string                  `json:"network" example:"tron"`
	DisplayName  string                  `json:"display_name" example:"TRON"`
	Tokens       []string                `json:"tokens" example:"USDT,USDC"`
	TokenDetails map[string]TokenDetails `json:"token_details"`
}

type EpayPublicConfig struct {
	DefaultToken    string `json:"default_token" example:""`       // EPay default token; ignored when a supported type=token.network selector is supplied. Empty means submit.php can create a status=4 placeholder when request token/network are also absent.
	DefaultCurrency string `json:"default_currency" example:"cny"` // EPay default fiat currency; still applies when a supported type selector is supplied and falls back to cny when unset.
	DefaultNetwork  string `json:"default_network" example:""`     // EPay default network; ignored when a supported type=token.network selector is supplied. Empty means submit.php can create a status=4 placeholder when request token/network are also absent.
}

type SitePublicConfig struct {
	WithdrawalReminderEnabled bool   `json:"withdrawal_reminder_enabled" example:"true"`
	CashierName               string `json:"cashier_name" example:"Acme Cashier"`
	LogoURL                   string `json:"logo_url" example:"https://cdn.example.com/logo.png"`
	WebsiteTitle              string `json:"website_title" example:"Acme Payments"`
	SupportLink               string `json:"support_link" example:"https://example.com/support"`
	BackgroundColor           string `json:"background_color" example:"#0f172a"`
	BackgroundImageURL        string `json:"background_image_url" example:"https://cdn.example.com/background.png"`
}

type OkPayPublicConfig struct {
	Enabled        bool     `json:"enabled" example:"true"`
	AllowTokens    []string `json:"allow_tokens" example:"USDT,TRX"`
	ShopID         string   `json:"shop_id,omitempty" example:"okpay-shop-test"`
	ShopToken      string   `json:"shop_token,omitempty" example:"secret-token"`
	APIURL         string   `json:"api_url,omitempty" example:"https://api.okaypay.me/shop/"`
	CallbackURL    string   `json:"callback_url,omitempty" example:"https://pay.example.com/payments/okpay/v1/notify"`
	ReturnURL      string   `json:"return_url,omitempty" example:"https://pay.example.com/success"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty" example:"10"`
}

// XLayerPublicConfig mirrors OkPayPublicConfig for the OKX Onchain OS
// Payment hosted checkout on X Layer. Public callers only get enabled +
// allow_tokens; credentials are filled for the admin variant only.
// There is deliberately no api_url field: the Onchain OS Payment base
// URL is the fixed xlayerAPIBaseURL constant in model/service, not a
// setting (2026-11-21 4th review).
type XLayerPublicConfig struct {
	Enabled        bool     `json:"enabled" example:"true"`
	AllowTokens    []string `json:"allow_tokens" example:"USDC,USDT0,USDG"`
	APIKey         string   `json:"api_key,omitempty" example:"okx-api-key"`
	APISecret      string   `json:"api_secret,omitempty" example:"okx-api-secret"`
	Passphrase     string   `json:"passphrase,omitempty" example:"okx-passphrase"`
	ReturnURL      string   `json:"return_url,omitempty" example:"https://pay.example.com/success"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty" example:"10"`
	ToAddress      string   `json:"to_address,omitempty" example:"0x0000000000000000000000000000000000000000"`
}

type PublicConfigResponse struct {
	SupportedAssets []NetworkTokenSupport `json:"supported_assets"`
	Site            SitePublicConfig      `json:"site"`
	Epay            EpayPublicConfig      `json:"epay"`
	OkPay           OkPayPublicConfig     `json:"okpay"`
	XLayer          XLayerPublicConfig    `json:"xlayer"`
	Version         string                `json:"version" example:"v1.0.1"`
}
