package comm

import (
	"slices"
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

func buildSupportedAssets(order *mdb.Orders) ([]response.NetworkTokenSupport, error) {
	var reserved []mdb.Orders
	canCreate := true
	if order != nil && order.Status == mdb.StatusWaitPay {
		root := order
		if order.ParentTradeId != "" {
			var err error
			root, err = service.GetOrderInfoByTradeId(order.ParentTradeId)
			if err != nil {
				return nil, err
			}
		}
		children, err := data.GetAllSubOrders(root.TradeId)
		if err != nil {
			return nil, err
		}
		canCreate = len(children) < service.MaxSubOrders
		reserved = append(children, *root)
	}
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
		network := strings.ToLower(w.Network)
		if order != nil {
			if _, err := data.ReceivingAddress(network, w.Address); err != nil {
				continue
			}
		}
		networkHasWallet[network] = struct{}{}
	}

	payable := make(map[string]bool)
	supports := make([]response.NetworkTokenSupport, 0, len(chains))
	for _, ch := range chains {
		if !canCreate {
			break
		}
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
		details := make(map[string]response.TokenDetails, len(tokens))
		for _, t := range tokens {
			if !service.ChainTokenReadyForPayment(t) {
				continue
			}
			sym := strings.ToUpper(strings.TrimSpace(t.Symbol))
			if sym == "" {
				continue
			}
			if order != nil {
				if _, seen := payable[sym]; !seen {
					_, quoteErr := service.OnChainTokenAmount(order.Amount, order.Currency, sym)
					payable[sym] = quoteErr == nil
				}
				if !payable[sym] {
					continue
				}
			}
			symbols = append(symbols, sym)
			details[sym] = response.TokenDetails{ContractAddress: t.ContractAddress, Decimals: t.Decimals}
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
			Network:      network,
			DisplayName:  displayName,
			Tokens:       symbols,
			TokenDetails: details,
		})
	}

	// Switching can reuse an existing reservation even if the chain or wallet
	// has since been disabled or the rate is temporarily unavailable.
	for _, row := range reserved {
		if row.Status != mdb.StatusWaitPay || row.ReceiveAddress == "" || row.ActualAmount <= 0 ||
			(row.PayProvider != "" && row.PayProvider != mdb.PaymentProviderOnChain) || row.Network == "" || row.Token == "" {
			continue
		}
		network, token := strings.ToLower(row.Network), strings.ToUpper(row.Token)
		i := slices.IndexFunc(supports, func(s response.NetworkTokenSupport) bool { return s.Network == network })
		if i < 0 {
			i = len(supports)
			supports = append(supports, response.NetworkTokenSupport{
				Network: network, DisplayName: network, TokenDetails: make(map[string]response.TokenDetails),
			})
		}
		support := &supports[i]
		if slices.Contains(support.Tokens, token) {
			continue
		}
		support.Tokens = append(support.Tokens, token)
		sort.Strings(support.Tokens)
		configured, err := data.GetConfiguredChainToken(network, token)
		if err != nil {
			return nil, err
		}
		if configured.ID != 0 {
			support.TokenDetails[token] = response.TokenDetails{ContractAddress: configured.ContractAddress, Decimals: configured.Decimals}
		}
	}
	return supports, nil
}

// GetPublicConfig returns payment/site config consumed by cashier/frontends.
// Hosted providers are advertised separately from on-chain network/token pairs.
// @Summary      Get public payment config
// @Description  Returns supported_assets for on-chain payments, site and epay defaults, and separate okpay and xlayer hosted payment settings. The xlayer allow_tokens list controls the tokens shown for OKX OnchainOS.
// @Description  The authenticated admin variant (/admin/api/v1/config) also includes provider credentials, internal callback settings and the merchant X Layer receiving address.
// @Param        trade_id query string false "Public cashier only. Filter on-chain options using the stored order currency and amount."
// @Tags         Payment Config
// @Produce      json
// @Success      200 {object} response.ApiResponse{data=response.PublicConfigResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /payments/gmpay/v1/config [get]
// @Router       /admin/api/v1/config [get]
func (c *BaseCommController) GetPublicConfig(ctx echo.Context) error {
	var order *mdb.Orders
	if tradeID := strings.TrimSpace(ctx.QueryParam("trade_id")); tradeID != "" && ctx.Get(middleware.AdminUserIDKey) == nil {
		existing, err := service.GetOrderInfoByTradeId(tradeID)
		if err != nil {
			return c.FailJson(ctx, err)
		}
		if existing.Status == mdb.StatusWaitPay || existing.Status == mdb.StatusWaitSelect {
			order = existing
		}
		ctx.Response().Header().Set("Cache-Control", "no-store")
	}
	supports, err := buildSupportedAssets(order)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	okpay := response.OkPayPublicConfig{
		Enabled:     data.GetOkPayEnabled(),
		AllowTokens: data.GetOkPayAllowTokens(),
	}
	xlayer := response.XLayerPublicConfig{
		Enabled:     data.GetXLayerEnabled() && data.XLayerConfigured(),
		AllowTokens: data.GetXLayerAllowTokens(),
	}
	if ctx.Get(middleware.AdminUserIDKey) != nil {
		okpay.ShopID = data.GetOkPayShopID()
		okpay.ShopToken = data.GetOkPayShopToken()
		okpay.APIURL = data.GetOkPayAPIURL()
		okpay.CallbackURL = data.GetOkPayCallbackURL()
		okpay.ReturnURL = data.GetOkPayReturnURL()
		okpay.TimeoutSeconds = data.GetOkPayTimeoutSeconds()
		xlayer.APIKey = data.GetXLayerAPIKey()
		xlayer.APISecret = data.GetXLayerAPISecret()
		xlayer.Passphrase = data.GetXLayerPassphrase()
		xlayer.ReturnURL = data.GetXLayerReturnURL()
		xlayer.TimeoutSeconds = data.GetXLayerTimeoutSeconds()
		xlayer.ToAddress = data.GetXLayerToAddress()
	}
	nativeOrigin := ""
	if origin := config.CashierScriptOrigin(); origin != nil && (strings.EqualFold(ctx.Request().Host, origin.Host) || strings.EqualFold(ctx.Request().Host, origin.Host+":443")) {
		nativeOrigin = origin.String()
	}
	return c.SucJson(ctx, response.PublicConfigResponse{
		SupportedAssets: supports,
		Site: response.SitePublicConfig{
			CashierScript:             data.GetSettingString(mdb.SettingKeyBrandCashierScript, ""),
			CashierScriptOrigin:       nativeOrigin,
			WithdrawalReminderEnabled: data.GetSettingBool(mdb.SettingKeyBrandWithdrawalReminder, false),
			PaymentReminderTitle:      data.GetSettingString(mdb.SettingKeyBrandReminderTitle, ""),
			PaymentReminderBody:       data.GetSettingString(mdb.SettingKeyBrandReminderBody, ""),
			CashierName:               data.GetBrandCashierName(),
			LogoURL:                   data.GetBrandLogoURL(),
			WebsiteTitle:              data.GetBrandWebsiteTitle(),
			SupportLink:               data.GetBrandSupportURL(),
			BackgroundColor:           data.GetBrandBackgroundColor(),
			BackgroundImageURL:        data.GetBrandBackgroundImageURL(),
		},
		Epay: response.EpayPublicConfig{
			DefaultToken:    data.GetSettingString(mdb.SettingKeyEpayDefaultToken, ""),
			DefaultCurrency: data.GetSettingString(mdb.SettingKeyEpayDefaultCurrency, "cny"),
			DefaultNetwork:  data.GetSettingString(mdb.SettingKeyEpayDefaultNetwork, ""),
		},
		OkPay:   okpay,
		XLayer:  xlayer,
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
