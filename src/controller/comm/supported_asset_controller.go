package comm

import (
	"sort"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/middleware"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/response"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/labstack/echo/v4"
)

func buildSupportedAssets() ([]response.NetworkTokenSupport, error) {
	chains, err := data.ListEnabledChains()
	if err != nil {
		return nil, err
	}
	wallets, err := data.GetAvailableWalletAddress()
	if err != nil {
		return nil, err
	}

	networkHasWallet := make(map[string]struct{})
	for _, w := range wallets {
		networkHasWallet[strings.ToLower(w.Network)] = struct{}{}
	}

	supports := make([]response.NetworkTokenSupport, 0, len(chains))
	for _, ch := range chains {
		network := strings.ToLower(ch.Network)
		if _, ok := networkHasWallet[network]; !ok {
			continue
		}
		tokens, err := data.ListEnabledChainTokensByNetwork(network)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 0 {
			continue
		}
		symbols := make([]string, 0, len(tokens))
		for _, t := range tokens {
			if !service.ChainTokenReadyForPayment(t) {
				continue
			}
			sym := strings.ToUpper(strings.TrimSpace(t.Symbol))
			if sym == "" {
				continue
			}
			symbols = append(symbols, sym)
		}
		if len(symbols) == 0 {
			continue
		}
		sort.Strings(symbols)
		displayName := strings.TrimSpace(ch.DisplayName)
		if displayName == "" {
			displayName = network
		}
		supports = append(supports, response.NetworkTokenSupport{
			Network:     network,
			DisplayName: displayName,
			Tokens:      symbols,
		})
	}

	return supports, nil
}

// GetPublicConfig returns payment/site config consumed by cashier/frontends.
// The public route returns brand/site display config plus non-sensitive
// epay/okpay knobs, while the authenticated admin route also includes OkPay
// credentials and internal URLs.
// @Summary      Get public payment config
// @Description  Returns supported_assets plus site, epay and okpay config used by cashier/frontends.
// @Description  The authenticated admin variant (/admin/api/v1/config) also includes OkPay shop credentials and internal callback settings.
// @Tags         Payment Config
// @Produce      json
// @Success      200 {object} response.ApiResponse{data=response.PublicConfigResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /payments/gmpay/v1/config [get]
// @Router       /admin/api/v1/config [get]
func (c *BaseCommController) GetPublicConfig(ctx echo.Context) error {
	supports, err := buildSupportedAssets()
	if err != nil {
		return c.FailJson(ctx, err)
	}
	okpay := response.OkPayPublicConfig{
		Enabled:     data.GetOkPayEnabled(),
		AllowTokens: data.GetOkPayAllowTokens(),
	}
	if ctx.Get(middleware.AdminUserIDKey) != nil {
		okpay.ShopID = data.GetOkPayShopID()
		okpay.ShopToken = data.GetOkPayShopToken()
		okpay.APIURL = data.GetOkPayAPIURL()
		okpay.CallbackURL = data.GetOkPayCallbackURL()
		okpay.ReturnURL = data.GetOkPayReturnURL()
		okpay.TimeoutSeconds = data.GetOkPayTimeoutSeconds()
	}
	return c.SucJson(ctx, response.PublicConfigResponse{
		SupportedAssets: supports,
		Site: response.SitePublicConfig{
			CashierName:        data.GetBrandCashierName(),
			LogoURL:            data.GetBrandLogoURL(),
			WebsiteTitle:       data.GetBrandWebsiteTitle(),
			SupportLink:        data.GetBrandSupportURL(),
			BackgroundColor:    data.GetBrandBackgroundColor(),
			BackgroundImageURL: data.GetBrandBackgroundImageURL(),
		},
		Epay: response.EpayPublicConfig{
			DefaultToken:    data.GetSettingString(mdb.SettingKeyEpayDefaultToken, ""),
			DefaultCurrency: data.GetSettingString(mdb.SettingKeyEpayDefaultCurrency, "cny"),
			DefaultNetwork:  data.GetSettingString(mdb.SettingKeyEpayDefaultNetwork, ""),
		},
		OkPay:   okpay,
		Version: config.GetAppVersion(),
	})
}

// ListSupportedAssetRecords is currently not wired to a public route.
// Kept as an internal helper candidate for future admin/debug use.
func (c *BaseCommController) ListSupportedAssetRecords(ctx echo.Context) error {
	network := ctx.QueryParam("network")
	list, err := data.ListEnabledChainTokens(network)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, list)
}

// GetSupportedAsset is currently not wired to a public route.
// Kept as an internal helper candidate for future admin/debug use.
func (c *BaseCommController) GetSupportedAsset(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	token, err := data.GetChainTokenByID(id)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if token.ID <= 0 || !token.Enabled {
		return c.FailJson(ctx, constant.SupportedAssetNotFound)
	}
	return c.SucJson(ctx, token)
}
