package dao

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/gookit/color"
	"gorm.io/gorm/clause"
)

var once sync.Once

// ResetMdbTableInitForTest resets the install/startup migration guard so tests
// can exercise fresh temporary databases in the same process.
func ResetMdbTableInitForTest() {
	once = sync.Once{}
}

// MdbTableInit performs AutoMigrate for all primary DB tables and seeds
// the minimum static rows: chains, chain tokens, default settings, and
// a Telegram notification channel migrated from legacy settings keys.
// Called by dao.Init (via bootstrap.InitApp) after the DB connection is
// established.  The install wizard (see install/installer.go) writes the
// .env file before bootstrap runs, so this function always finds the DB
// available.
func MdbTableInit() {
	once.Do(func() {
		migrations := []struct {
			name  string
			model interface{}
		}{
			{"Orders", &mdb.Orders{}},
			{"WalletAddress", &mdb.WalletAddress{}},
			{"AdminUser", &mdb.AdminUser{}},
			{"ApiKey", &mdb.ApiKey{}},
			{"Setting", &mdb.Setting{}},
			{"RateCache", &mdb.RateCache{}},
			{"NotificationChannel", &mdb.NotificationChannel{}},
			{"Chain", &mdb.Chain{}},
			{"ChainToken", &mdb.ChainToken{}},
			{"RpcNode", &mdb.RpcNode{}},
			{"ProviderOrder", &mdb.ProviderOrder{}},
		}
		for _, m := range migrations {
			if err := Mdb.AutoMigrate(m.model); err != nil {
				color.Red.Printf("[store_db] AutoMigrate DB(%s),err=%s\n", m.name, err)
				return
			}
		}

		seedChains()
		backfillRpcNodePurpose()
		seedRpcNodes()
		seedChainTokens()
		seedDefaultSettings()
		seedTelegramChannelFromSettings()
	})
}

// seedChains inserts the built-in networks as enabled rows. Uses
// ON CONFLICT DO NOTHING so re-runs are no-ops and admin edits persist.
func seedChains() {
	defaults := []mdb.Chain{
		{Network: mdb.NetworkTron, DisplayName: "TRON", Enabled: true, MinConfirmations: 1, ScanIntervalSec: 5},
		{Network: mdb.NetworkEthereum, DisplayName: "Ethereum", Enabled: true, MinConfirmations: 3, ScanIntervalSec: 5},
		{Network: mdb.NetworkSolana, DisplayName: "Solana", Enabled: true, MinConfirmations: 1, ScanIntervalSec: 5},
		{Network: mdb.NetworkBsc, DisplayName: "BSC", Enabled: true, MinConfirmations: 3, ScanIntervalSec: 5},
		{Network: mdb.NetworkPolygon, DisplayName: "Polygon", Enabled: true, MinConfirmations: 3, ScanIntervalSec: 5},
		{Network: mdb.NetworkPlasma, DisplayName: "Plasma", Enabled: true, MinConfirmations: 1, ScanIntervalSec: 5},
		{Network: mdb.NetworkTon, DisplayName: "TON", Enabled: true, MinConfirmations: 1, ScanIntervalSec: 5},
		{Network: mdb.NetworkAptos, DisplayName: "Aptos", Enabled: true, MinConfirmations: 1, ScanIntervalSec: 5},
	}
	if err := Mdb.Clauses(clause.OnConflict{DoNothing: true}).Create(&defaults).Error; err != nil {
		color.Red.Printf("[store_db] seed chains err=%s\n", err)
	}
}

// seedChainTokens inserts default token configurations for each chain.
// ON CONFLICT DO NOTHING on the (network, symbol) unique index so
// re-runs are no-ops and admin edits to contract_address / decimals /
// min_amount persist across restarts.
func seedChainTokens() {
	// Contract-address rows are matched by scanners via exact contract
	// lookup. Native-token rows (empty contract_address) are matched by
	// symbol — these act as admin toggles for the native payment path.
	defaults := []mdb.ChainToken{
		// TRON
		{Network: mdb.NetworkTron, Symbol: "USDT", ContractAddress: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkTron, Symbol: "TRX", ContractAddress: "", Decimals: 6, Enabled: true},
		// Ethereum
		{Network: mdb.NetworkEthereum, Symbol: "USDT", ContractAddress: "0xdAC17F958D2ee523a2206206994597C13D831ec7", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkEthereum, Symbol: "USDC", ContractAddress: "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", Decimals: 6, Enabled: true},
		// Solana
		{Network: mdb.NetworkSolana, Symbol: "USDT", ContractAddress: "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkSolana, Symbol: "USDC", ContractAddress: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkSolana, Symbol: "SOL", ContractAddress: "", Decimals: 9, Enabled: true},
		// BSC (BEP20 USDT/USDC are 18 decimals, unlike their Ethereum counterparts)
		{Network: mdb.NetworkBsc, Symbol: "USDT", ContractAddress: "0x55d398326f99059fF775485246999027B3197955", Decimals: 18, Enabled: true},
		{Network: mdb.NetworkBsc, Symbol: "USDC", ContractAddress: "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d", Decimals: 18, Enabled: true},
		// Polygon
		{Network: mdb.NetworkPolygon, Symbol: "USDT", ContractAddress: "0xc2132D05D31c914a87C6611C10748AEb04B58e8F", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkPolygon, Symbol: "USDC", ContractAddress: "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkPolygon, Symbol: "USDC.e", ContractAddress: "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174", Decimals: 6, Enabled: true},
		// Plasma
		{Network: mdb.NetworkPlasma, Symbol: "USDT", ContractAddress: "0xB8CE59FC3717ada4C02eaDF9682A9e934F625ebb", Decimals: 6, Enabled: true},
		// TON
		{Network: mdb.NetworkTon, Symbol: "TON", ContractAddress: "", Decimals: 9, Enabled: true},
		{Network: mdb.NetworkTon, Symbol: "USDT", ContractAddress: "0:b113a994b5024a16719f69139328eb759596c38a25f59028b146fecdc3621dfe", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkAptos, Symbol: "USDC", ContractAddress: "0xbae207659db88bea0cbead6da0ed00aac12edcdda169e591cd41c94180b46f3b", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkAptos, Symbol: "USDT", ContractAddress: "0x357b0b74bc833e95a115ad22604854d6b0fca151cecd94111770e5d6ffc9dc2b", Decimals: 6, Enabled: true},
	}
	if err := Mdb.Clauses(clause.OnConflict{DoNothing: true}).Create(&defaults).Error; err != nil {
		color.Red.Printf("[store_db] seed chain_tokens err=%s\n", err)
	}
}

// seedRpcNodes inserts default RPC endpoints for each chain. Checks per
// (network, url) so re-runs are idempotent and admin edits persist.
func seedRpcNodes() {
	defaults := defaultRpcNodes()
	for _, d := range defaults {
		var count int64
		if err := Mdb.Model(&mdb.RpcNode{}).
			Where("network = ? AND url = ?", d.Network, d.Url).
			Count(&count).Error; err != nil {
			color.Red.Printf("[store_db] seed rpc_nodes check err=%s\n", err)
			continue
		}
		if count > 0 {
			continue
		}
		if err := Mdb.Create(&d).Error; err != nil {
			color.Red.Printf("[store_db] seed rpc_nodes err=%s\n", err)
		}
	}
}

func defaultRpcNodes() []mdb.RpcNode {
	defaults := []mdb.RpcNode{
		{Network: mdb.NetworkTron, Url: "https://api.trongrid.io", Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkEthereum, Url: "wss://ethereum.publicnode.com", Type: mdb.RpcNodeTypeWs, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkSolana, Url: "https://api.mainnet-beta.solana.com", Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkBsc, Url: "wss://bsc.drpc.org", Type: mdb.RpcNodeTypeWs, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkPolygon, Url: "wss://polygon-bor-rpc.publicnode.com", Type: mdb.RpcNodeTypeWs, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkPlasma, Url: "wss://rpc.plasma.to", Type: mdb.RpcNodeTypeWs, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkTon, Url: "https://ton-blockchain.github.io/global.config.json", Type: mdb.RpcNodeTypeLite, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkAptos, Url: "https://aptos-rest.publicnode.com/", Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkEthereum, Url: "https://rpc.epusdt.com/ethereum", Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeManualVerify, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkBsc, Url: "https://rpc.epusdt.com/binance", Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeManualVerify, Status: mdb.RpcNodeStatusUnknown},
		{Network: mdb.NetworkPolygon, Url: "https://rpc.epusdt.com/polygon", Type: mdb.RpcNodeTypeHttp, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeManualVerify, Status: mdb.RpcNodeStatusUnknown},
	}
	return defaults
}

func backfillRpcNodePurpose() {
	if err := Mdb.Model(&mdb.RpcNode{}).
		Where("purpose = '' OR purpose IS NULL").
		Update("purpose", mdb.RpcNodePurposeGeneral).Error; err != nil {
		color.Red.Printf("[store_db] backfill rpc_nodes purpose err=%s\n", err)
	}
}

// seedDefaultSettings inserts built-in default settings.
// Uses ON CONFLICT DO NOTHING so admin edits persist across restarts.
func seedDefaultSettings() {
	okPayCallbackURL := strings.TrimRight(strings.TrimSpace(config.GetAppUri()), "/")
	if okPayCallbackURL != "" {
		okPayCallbackURL += "/payments/okpay/v1/notify"
	}
	defaultRateMode := defaultRateModeForSeed()
	defaults := []mdb.Setting{
		{Group: mdb.SettingGroupSystem, Key: mdb.SettingKeyAmountPrecision, Value: "2", Type: mdb.SettingTypeInt},
		{Group: mdb.SettingGroupSystem, Key: mdb.SettingKeySystemLogLevel, Value: mdb.SettingDefaultSystemLogLevel, Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupRate, Key: mdb.SettingKeyRateForcedRateList, Value: mdb.SettingDefaultRateForcedRateList, Type: mdb.SettingTypeJSON},
		{Group: mdb.SettingGroupRate, Key: mdb.SettingKeyRateMode, Value: defaultRateMode, Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupRate, Key: mdb.SettingKeyRateCacheTTLSeconds, Value: strconv.Itoa(mdb.SettingDefaultRateCacheTTL), Type: mdb.SettingTypeInt},
		{Group: mdb.SettingGroupEpay, Key: mdb.SettingKeyEpayDefaultToken, Value: "", Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupEpay, Key: mdb.SettingKeyEpayDefaultCurrency, Value: "cny", Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupEpay, Key: mdb.SettingKeyEpayDefaultNetwork, Value: "", Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupOkPay, Key: mdb.SettingKeyOkPayEnabled, Value: "false", Type: mdb.SettingTypeBool},
		{Group: mdb.SettingGroupOkPay, Key: mdb.SettingKeyOkPayAPIURL, Value: "https://api.okaypay.me/shop/", Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupOkPay, Key: mdb.SettingKeyOkPayCallbackURL, Value: okPayCallbackURL, Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupOkPay, Key: mdb.SettingKeyOkPayTimeoutSeconds, Value: "10", Type: mdb.SettingTypeInt},
		{Group: mdb.SettingGroupOkPay, Key: mdb.SettingKeyOkPayAllowTokens, Value: "USDT,TRX", Type: mdb.SettingTypeString},
	}
	if err := Mdb.Clauses(clause.OnConflict{DoNothing: true}).Create(&defaults).Error; err != nil {
		color.Red.Printf("[store_db] seed default settings err=%s\n", err)
	}
}

// defaultRateModeForSeed keeps fresh installations on the safe fixed default,
// while migrating installations created before rate.mode existed to auto when
// they already relied on an external rate API. This is the closest equivalent
// to the legacy forced-rate-then-API-fallback behavior available with the new
// two explicit modes.
func defaultRateModeForSeed() string {
	var existingSettings int64
	if err := Mdb.Unscoped().Model(&mdb.Setting{}).Count(&existingSettings).Error; err != nil {
		color.Red.Printf("[store_db] detect existing settings for rate mode err=%s\n", err)
		return mdb.SettingDefaultRateMode
	}
	if existingSettings == 0 {
		return mdb.SettingDefaultRateMode
	}

	var apiSetting mdb.Setting
	if err := Mdb.Where("key = ?", mdb.SettingKeyRateApiUrl).Limit(1).Find(&apiSetting).Error; err != nil {
		color.Red.Printf("[store_db] detect legacy rate api setting err=%s\n", err)
	}
	if strings.TrimSpace(apiSetting.Value) != "" || strings.TrimSpace(config.GetRateApiUrlFromEnv()) != "" {
		return config.RateModeAuto
	}
	return mdb.SettingDefaultRateMode
}

// seedTelegramChannelFromSettings auto-migrates the legacy settings-table
// Telegram config into a notification_channels row so the notify dispatcher
// can use it. Idempotent: no-op when any telegram channel already exists.
//
// Legacy keys read from settings:
//
//	system.telegram_bot_token               → TelegramConfig.BotToken
//	system.telegram_chat_id                 → TelegramConfig.ChatID
//	system.telegram_payment_notice_enabled  → events.pay_success
//	system.telegram_abnormal_notice_enabled → events.order_expired
func seedTelegramChannelFromSettings() {
	var count int64
	if err := Mdb.Model(&mdb.NotificationChannel{}).
		Where("type = ?", mdb.NotificationTypeTelegram).
		Count(&count).Error; err != nil {
		color.Red.Printf("[store_db] seed telegram channel check err=%s\n", err)
		return
	}
	if count > 0 {
		return
	}
	SyncTelegramChannelFromSettings()
}

// SyncTelegramChannelFromSettings reads the four Telegram keys from the
// settings table and upserts the matching notification_channels row:
//   - creates a new row when none exists yet
//   - updates config+events on the first existing row when settings change
//
// This is called at startup (via seedTelegramChannelFromSettings) and by
// the settings controller whenever any Telegram key is changed at runtime.
func SyncTelegramChannelFromSettings() {
	keys := []string{
		"system.telegram_bot_token",
		"system.telegram_chat_id",
		"system.telegram_payment_notice_enabled",
		"system.telegram_abnormal_notice_enabled",
	}
	var rows []mdb.Setting
	if err := Mdb.Where("key IN ?", keys).Find(&rows).Error; err != nil {
		color.Red.Printf("[store_db] sync telegram channel: read settings err=%s\n", err)
		return
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}

	botToken := strings.TrimSpace(m["system.telegram_bot_token"])
	chatIDStr := strings.TrimSpace(m["system.telegram_chat_id"])
	if botToken == "" || chatIDStr == "" {
		return // No telegram config to sync.
	}

	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		color.Red.Printf("[store_db] sync telegram channel: invalid chat_id=%q\n", chatIDStr)
		return
	}

	paymentEnabled, _ := strconv.ParseBool(m["system.telegram_payment_notice_enabled"])
	abnormalEnabled, _ := strconv.ParseBool(m["system.telegram_abnormal_notice_enabled"])

	configJSON, _ := json.Marshal(map[string]interface{}{
		"bot_token": botToken,
		"chat_id":   chatID,
		"proxy":     "",
	})
	eventsJSON, _ := json.Marshal(map[string]bool{
		mdb.NotifyEventPaySuccess:   paymentEnabled,
		mdb.NotifyEventOrderExpired: abnormalEnabled,
		mdb.NotifyEventDailyReport:  false,
	})

	// Try to update an existing row first.
	// Use Find (not First) to avoid GORM printing a spurious
	// "record not found" log when the table is empty on first boot.
	var existing mdb.NotificationChannel
	Mdb.Model(&mdb.NotificationChannel{}).
		Where("type = ?", mdb.NotificationTypeTelegram).
		Order("id ASC").Limit(1).Find(&existing)
	if existing.ID != 0 {
		// Row exists — patch config and events.
		if err2 := Mdb.Model(&existing).Updates(map[string]interface{}{
			"config": string(configJSON),
			"events": string(eventsJSON),
		}).Error; err2 != nil {
			color.Red.Printf("[store_db] sync telegram channel update err=%s\n", err2)
		}
		return
	}

	// No row yet — create one.
	ch := mdb.NotificationChannel{
		Type:    mdb.NotificationTypeTelegram,
		Name:    "Telegram",
		Config:  string(configJSON),
		Events:  string(eventsJSON),
		Enabled: true,
	}
	if err := Mdb.Create(&ch).Error; err != nil {
		color.Red.Printf("[store_db] sync telegram channel create err=%s\n", err)
	} else {
		color.Green.Printf("[store_db] created telegram notification_channel from settings (id=%d)\n", ch.ID)
	}
}
