package response

type NetworkTokenSupport struct {
	Network     string   `json:"network" example:"tron"`
	DisplayName string   `json:"display_name" example:"TRON"`
	Tokens      []string `json:"tokens" example:"USDT,USDC"`
}

type EpayPublicConfig struct {
	DefaultToken    string `json:"default_token" example:""`       // EPay default token; ignored when a supported type=token.network selector is supplied. Empty means submit.php can create a status=4 placeholder when request token/network are also absent.
	DefaultCurrency string `json:"default_currency" example:"cny"` // EPay default fiat currency; still applies when a supported type selector is supplied and falls back to cny when unset.
	DefaultNetwork  string `json:"default_network" example:""`     // EPay default network; ignored when a supported type=token.network selector is supplied. Empty means submit.php can create a status=4 placeholder when request token/network are also absent.
}

type SitePublicConfig struct {
	CashierName        string `json:"cashier_name" example:"Acme Cashier"`
	LogoURL            string `json:"logo_url" example:"https://cdn.example.com/logo.png"`
	WebsiteTitle       string `json:"website_title" example:"Acme Payments"`
	SupportLink        string `json:"support_link" example:"https://example.com/support"`
	BackgroundColor    string `json:"background_color" example:"#0f172a"`
	BackgroundImageURL string `json:"background_image_url" example:"https://cdn.example.com/background.png"`
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

type PublicConfigResponse struct {
	SupportedAssets []NetworkTokenSupport `json:"supported_assets"`
	Site            SitePublicConfig      `json:"site"`
	Epay            EpayPublicConfig      `json:"epay"`
	OkPay           OkPayPublicConfig     `json:"okpay"`
	Version         string                `json:"version" example:"v1.0.1"`
}
