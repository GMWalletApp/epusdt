package dao

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/libtnb/sqlite"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

func TestDefaultRpcNodesIncludesManualVerifyEpusdtEvmNodes(t *testing.T) {
	want := map[string]string{
		mdb.NetworkEthereum: "https://rpc.epusdt.com/ethereum",
		mdb.NetworkBsc:      "https://rpc.epusdt.com/binance",
		mdb.NetworkPolygon:  "https://rpc.epusdt.com/polygon",
	}
	got := make(map[string]mdb.RpcNode)
	for _, node := range defaultRpcNodes() {
		if node.Purpose != mdb.RpcNodePurposeManualVerify {
			continue
		}
		if _, ok := want[node.Network]; ok {
			got[node.Network] = node
		}
	}

	for network, url := range want {
		node, ok := got[network]
		if !ok {
			t.Fatalf("missing manual_verify seed rpc node for %s", network)
		}
		if node.Url != url {
			t.Fatalf("%s manual_verify seed url = %q, want %q", network, node.Url, url)
		}
		if node.Type != mdb.RpcNodeTypeHttp {
			t.Fatalf("%s manual_verify seed type = %q, want %q", network, node.Type, mdb.RpcNodeTypeHttp)
		}
		if !node.Enabled {
			t.Fatalf("%s manual_verify seed enabled = false, want true", network)
		}
		if node.Status != mdb.RpcNodeStatusUnknown {
			t.Fatalf("%s manual_verify seed status = %q, want %q", network, node.Status, mdb.RpcNodeStatusUnknown)
		}
	}
}

func TestDefaultRpcNodesIncludesTonLiteGeneralNode(t *testing.T) {
	var got *mdb.RpcNode
	nodes := defaultRpcNodes()
	for i := range nodes {
		node := nodes[i]
		if node.Network == mdb.NetworkTon && node.Type == mdb.RpcNodeTypeLite {
			got = &node
			break
		}
	}

	if got == nil {
		t.Fatal("missing TON lite seed rpc node")
	}
	if got.Url != "https://ton-blockchain.github.io/global.config.json" {
		t.Fatalf("TON lite seed url = %q", got.Url)
	}
	if got.Purpose != mdb.RpcNodePurposeGeneral {
		t.Fatalf("TON lite seed purpose = %q, want %q", got.Purpose, mdb.RpcNodePurposeGeneral)
	}
	if !got.Enabled {
		t.Fatal("TON lite seed enabled = false, want true")
	}
	if got.Status != mdb.RpcNodeStatusUnknown {
		t.Fatalf("TON lite seed status = %q, want %q", got.Status, mdb.RpcNodeStatusUnknown)
	}
}

func TestDefaultRpcNodesIncludesAptosPublicNode(t *testing.T) {
	var got *mdb.RpcNode
	nodes := defaultRpcNodes()
	for i := range nodes {
		node := nodes[i]
		if node.Network == mdb.NetworkAptos {
			got = &node
			break
		}
	}

	if got == nil {
		t.Fatal("missing Aptos seed rpc node")
	}
	if got.Url != "https://aptos-rest.publicnode.com/" {
		t.Fatalf("Aptos seed url = %q", got.Url)
	}
	if got.Type != mdb.RpcNodeTypeHttp {
		t.Fatalf("Aptos seed type = %q, want %q", got.Type, mdb.RpcNodeTypeHttp)
	}
	if got.Purpose != mdb.RpcNodePurposeGeneral {
		t.Fatalf("Aptos seed purpose = %q, want %q", got.Purpose, mdb.RpcNodePurposeGeneral)
	}
	if !got.Enabled {
		t.Fatal("Aptos seed enabled = false, want true")
	}
	if got.Status != mdb.RpcNodeStatusUnknown {
		t.Fatalf("Aptos seed status = %q, want %q", got.Status, mdb.RpcNodeStatusUnknown)
	}
}

func TestSeedChainsIncludesAptos(t *testing.T) {
	db := setupSeedTableTestDB(t, &mdb.Chain{})
	Mdb = db

	seedChains()

	var row mdb.Chain
	if err := Mdb.Where("network = ?", mdb.NetworkAptos).Take(&row).Error; err != nil {
		t.Fatalf("load Aptos chain seed: %v", err)
	}
	if !row.Enabled {
		t.Fatal("Aptos chain enabled = false, want true")
	}
}

func TestSeedChainsIncludesXLayer(t *testing.T) {
	db := setupSeedTableTestDB(t, &mdb.Chain{})
	Mdb = db

	seedChains()

	var row mdb.Chain
	if err := Mdb.Where("network = ?", mdb.NetworkXLayer).Take(&row).Error; err != nil {
		t.Fatalf("load X Layer chain seed: %v", err)
	}
	if !row.Enabled {
		t.Fatal("X Layer chain enabled = false, want true")
	}
	if row.DisplayName != "X Layer" {
		t.Fatalf("X Layer display_name = %q, want %q", row.DisplayName, "X Layer")
	}
}

func TestSeedChainTokensIncludesXLayerStablecoins(t *testing.T) {
	db := setupSeedTableTestDB(t, &mdb.ChainToken{})
	Mdb = db

	seedChainTokens()

	wantContract := map[string]string{
		"USDC":  "0xb6ceceab302e2e4948951ee7843fc24e92933061",
		"USDT0": "0x779ded0c9e1022225f8e0630b35a9b54be713736",
		"USDG":  "0x4ae46a509f6b1d9056937ba4500cb143933d2dc8",
	}
	for symbol, contract := range wantContract {
		var row mdb.ChainToken
		if err := Mdb.Where("network = ? AND symbol = ?", mdb.NetworkXLayer, symbol).Take(&row).Error; err != nil {
			t.Fatalf("load X Layer %s token seed: %v", symbol, err)
		}
		if !row.Enabled {
			t.Fatalf("X Layer %s enabled = false, want true", symbol)
		}
		if row.ContractAddress != contract {
			t.Fatalf("X Layer %s contract_address = %q, want %q", symbol, row.ContractAddress, contract)
		}
		if row.Decimals != 6 {
			t.Fatalf("X Layer %s decimals = %d, want 6", symbol, row.Decimals)
		}
	}
}

func TestSeedXLayerWalletFromSettings(t *testing.T) {
	db := setupSeedTableTestDB(t, &mdb.Setting{}, &mdb.WalletAddress{})
	Mdb = db

	// No settings key and no wallet: nothing to do.
	seedXLayerWalletFromSettings()
	var count int64
	Mdb.Model(&mdb.WalletAddress{}).Where("network = ?", mdb.NetworkXLayer).Count(&count)
	if count != 0 {
		t.Fatalf("wallet rows = %d, want 0 when no legacy setting exists", count)
	}

	// With the legacy key set, the value migrates into an enabled wallet row.
	if err := Mdb.Create(&mdb.Setting{
		Group: mdb.SettingGroupXLayer,
		Key:   mdb.SettingKeyXLayerToAddress,
		Value: " 0xAbC0000000000000000000000000000000000001 ",
		Type:  mdb.SettingTypeString,
	}).Error; err != nil {
		t.Fatalf("seed legacy to_address: %v", err)
	}
	seedXLayerWalletFromSettings()
	var row mdb.WalletAddress
	if err := Mdb.Where("network = ?", mdb.NetworkXLayer).Take(&row).Error; err != nil {
		t.Fatalf("load migrated wallet: %v", err)
	}
	if row.Address != "0xAbC0000000000000000000000000000000000001" {
		t.Fatalf("migrated address = %q, want trimmed legacy value", row.Address)
	}
	if row.Status != mdb.TokenStatusEnable || row.Source != mdb.WalletSourceManual {
		t.Fatalf("migrated wallet status=%d source=%q, want enabled/manual", row.Status, row.Source)
	}

	// Re-running must not create a duplicate.
	seedXLayerWalletFromSettings()
	Mdb.Model(&mdb.WalletAddress{}).Where("network = ?", mdb.NetworkXLayer).Count(&count)
	if count != 1 {
		t.Fatalf("wallet rows = %d after re-seed, want 1", count)
	}
}

func TestSeedXLayerWalletRestoresSoftDeletedRow(t *testing.T) {
	db := setupSeedTableTestDB(t, &mdb.Setting{}, &mdb.WalletAddress{})
	Mdb = db

	// A soft-deleted row would violate the (network, address) unique index
	// if the seed tried to insert the same address again; it must be
	// restored instead.
	deleted := &mdb.WalletAddress{
		Network: mdb.NetworkXLayer,
		Address: "0xAbC0000000000000000000000000000000000002",
		Status:  mdb.TokenStatusDisable,
	}
	if err := Mdb.Create(deleted).Error; err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if err := Mdb.Delete(deleted).Error; err != nil {
		t.Fatalf("soft-delete wallet: %v", err)
	}
	if err := Mdb.Create(&mdb.Setting{
		Group: mdb.SettingGroupXLayer,
		Key:   mdb.SettingKeyXLayerToAddress,
		Value: "0xAbC0000000000000000000000000000000000002",
		Type:  mdb.SettingTypeString,
	}).Error; err != nil {
		t.Fatalf("seed legacy to_address: %v", err)
	}

	seedXLayerWalletFromSettings()

	var count int64
	Mdb.Unscoped().Model(&mdb.WalletAddress{}).Where("network = ?", mdb.NetworkXLayer).Count(&count)
	if count != 1 {
		t.Fatalf("wallet rows (incl. soft-deleted) = %d, want 1", count)
	}
	var row mdb.WalletAddress
	if err := Mdb.Unscoped().Where("network = ?", mdb.NetworkXLayer).Take(&row).Error; err != nil {
		t.Fatalf("load restored wallet: %v", err)
	}
	if row.DeletedAt.Valid {
		t.Fatal("restored wallet still soft-deleted")
	}
	if row.Status != mdb.TokenStatusEnable {
		t.Fatalf("restored wallet status = %d, want enabled", row.Status)
	}
}

func TestSeedChainTokensIncludesAptosAssets(t *testing.T) {
	db := setupSeedTableTestDB(t, &mdb.ChainToken{})
	Mdb = db

	seedChainTokens()

	wantEnabled := map[string]bool{
		mdb.NetworkAptos + "/USDC": true,
		mdb.NetworkAptos + "/USDT": true,
	}
	wantContract := map[string]string{
		mdb.NetworkAptos + "/USDT": "0x357b0b74bc833e95a115ad22604854d6b0fca151cecd94111770e5d6ffc9dc2b",
	}
	for key, enabled := range wantEnabled {
		parts := strings.Split(key, "/")
		var row mdb.ChainToken
		if err := Mdb.Where("network = ? AND symbol = ?", parts[0], parts[1]).Take(&row).Error; err != nil {
			t.Fatalf("load token seed %s: %v", key, err)
		}
		if row.Enabled != enabled {
			t.Fatalf("%s enabled = %v, want %v", key, row.Enabled, enabled)
		}
		if want, ok := wantContract[key]; ok && row.ContractAddress != want {
			t.Fatalf("%s contract_address = %q, want %q", key, row.ContractAddress, want)
		}
	}
}

func TestSeedDefaultSettingsIncludesSystemLogLevel(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db

	seedDefaultSettings()

	var row mdb.Setting
	if err := Mdb.Where("`key` = ?", mdb.SettingKeySystemLogLevel).Take(&row).Error; err != nil {
		t.Fatalf("load system.log_level seed: %v", err)
	}
	if row.Group != mdb.SettingGroupSystem {
		t.Fatalf("system.log_level group = %q, want %q", row.Group, mdb.SettingGroupSystem)
	}
	if row.Value != mdb.SettingDefaultSystemLogLevel {
		t.Fatalf("system.log_level value = %q, want %q", row.Value, mdb.SettingDefaultSystemLogLevel)
	}
	if row.Type != mdb.SettingTypeString {
		t.Fatalf("system.log_level type = %q, want %q", row.Type, mdb.SettingTypeString)
	}
}

func TestSeedDefaultSettingsIncludesDefaultForcedRateList(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db

	seedDefaultSettings()

	var row mdb.Setting
	if err := Mdb.Where("`key` = ?", mdb.SettingKeyRateForcedRateList).Take(&row).Error; err != nil {
		t.Fatalf("load rate.forced_rate_list seed: %v", err)
	}
	if row.Group != mdb.SettingGroupRate {
		t.Fatalf("rate.forced_rate_list group = %q, want %q", row.Group, mdb.SettingGroupRate)
	}
	if row.Value != mdb.SettingDefaultRateForcedRateList {
		t.Fatalf("rate.forced_rate_list value = %q, want %q", row.Value, mdb.SettingDefaultRateForcedRateList)
	}
	if row.Type != mdb.SettingTypeJSON {
		t.Fatalf("rate.forced_rate_list type = %q, want %q", row.Type, mdb.SettingTypeJSON)
	}
}

func TestSeedDefaultSettingsIncludesRateModeAndTTL(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db

	seedDefaultSettings()

	wants := map[string]struct {
		value     string
		valueType string
	}{
		mdb.SettingKeyRateMode:            {value: mdb.SettingDefaultRateMode, valueType: mdb.SettingTypeString},
		mdb.SettingKeyRateCacheTTLSeconds: {value: "300", valueType: mdb.SettingTypeInt},
	}
	for key, want := range wants {
		var row mdb.Setting
		if err := Mdb.Where("`key` = ?", key).Take(&row).Error; err != nil {
			t.Fatalf("load %s seed: %v", key, err)
		}
		if row.Group != mdb.SettingGroupRate || row.Value != want.value || row.Type != want.valueType {
			t.Fatalf("%s seed = group:%q value:%q type:%q", key, row.Group, row.Value, row.Type)
		}
	}
}

func TestSeedDefaultSettingsKeepsFreshInstallFixedWithConfiguredEnvAPI(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db
	viper.Set("api_rate_url", "https://rate.example.test")

	seedDefaultSettings()

	var row mdb.Setting
	if err := Mdb.Where("`key` = ?", mdb.SettingKeyRateMode).Take(&row).Error; err != nil {
		t.Fatalf("load fresh rate.mode seed: %v", err)
	}
	if row.Value != config.RateModeFixed {
		t.Fatalf("fresh rate.mode = %q, want fixed", row.Value)
	}
}

func TestSeedDefaultSettingsMigratesLegacyExternalAPIInstallToAuto(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db
	legacy := []mdb.Setting{
		{Group: mdb.SettingGroupRate, Key: mdb.SettingKeyRateForcedRateList, Value: mdb.SettingDefaultRateForcedRateList, Type: mdb.SettingTypeJSON},
		{Group: mdb.SettingGroupRate, Key: mdb.SettingKeyRateApiUrl, Value: "https://rate.example.test", Type: mdb.SettingTypeString},
	}
	if err := Mdb.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy settings: %v", err)
	}

	seedDefaultSettings()

	var row mdb.Setting
	if err := Mdb.Where("`key` = ?", mdb.SettingKeyRateMode).Take(&row).Error; err != nil {
		t.Fatalf("load migrated rate.mode: %v", err)
	}
	if row.Value != config.RateModeAuto {
		t.Fatalf("legacy rate.mode = %q, want auto", row.Value)
	}
}

func TestSeedDefaultSettingsMigratesLegacyEnvAPIInstallToAuto(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db
	viper.Set("api_rate_url", "https://rate.example.test")
	if err := Mdb.Create(&mdb.Setting{
		Group: mdb.SettingGroupRate,
		Key:   mdb.SettingKeyRateForcedRateList,
		Value: mdb.SettingDefaultRateForcedRateList,
		Type:  mdb.SettingTypeJSON,
	}).Error; err != nil {
		t.Fatalf("seed legacy forced rate: %v", err)
	}

	seedDefaultSettings()

	var row mdb.Setting
	if err := Mdb.Where("`key` = ?", mdb.SettingKeyRateMode).Take(&row).Error; err != nil {
		t.Fatalf("load env-migrated rate.mode: %v", err)
	}
	if row.Value != config.RateModeAuto {
		t.Fatalf("legacy env rate.mode = %q, want auto", row.Value)
	}
}

func TestSeedDefaultSettingsPreservesExplicitLegacyRateMode(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db
	legacy := []mdb.Setting{
		{Group: mdb.SettingGroupRate, Key: mdb.SettingKeyRateApiUrl, Value: "https://rate.example.test", Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupRate, Key: mdb.SettingKeyRateMode, Value: config.RateModeFixed, Type: mdb.SettingTypeString},
	}
	if err := Mdb.Create(&legacy).Error; err != nil {
		t.Fatalf("seed explicit mode settings: %v", err)
	}

	seedDefaultSettings()

	var row mdb.Setting
	if err := Mdb.Where("`key` = ?", mdb.SettingKeyRateMode).Take(&row).Error; err != nil {
		t.Fatalf("load explicit rate.mode: %v", err)
	}
	if row.Value != config.RateModeFixed {
		t.Fatalf("explicit rate.mode = %q, want fixed", row.Value)
	}
}

func TestSeedDefaultSettingsUsesEmptyEpayTokenAndNetwork(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db

	seedDefaultSettings()

	rows := make(map[string]mdb.Setting)
	for _, key := range []string{
		mdb.SettingKeyEpayDefaultToken,
		mdb.SettingKeyEpayDefaultCurrency,
		mdb.SettingKeyEpayDefaultNetwork,
	} {
		var row mdb.Setting
		if err := Mdb.Where("`key` = ?", key).Take(&row).Error; err != nil {
			t.Fatalf("load %s seed: %v", key, err)
		}
		rows[key] = row
	}
	if rows[mdb.SettingKeyEpayDefaultToken].Value != "" {
		t.Fatalf("epay.default_token seed = %q, want empty", rows[mdb.SettingKeyEpayDefaultToken].Value)
	}
	if rows[mdb.SettingKeyEpayDefaultNetwork].Value != "" {
		t.Fatalf("epay.default_network seed = %q, want empty", rows[mdb.SettingKeyEpayDefaultNetwork].Value)
	}
	if rows[mdb.SettingKeyEpayDefaultCurrency].Value != "cny" {
		t.Fatalf("epay.default_currency seed = %q, want cny", rows[mdb.SettingKeyEpayDefaultCurrency].Value)
	}
}

func TestSeedDefaultSettingsDoesNotOverwriteExistingEpayDefaults(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db
	for _, row := range []mdb.Setting{
		{Group: mdb.SettingGroupEpay, Key: mdb.SettingKeyEpayDefaultToken, Value: "usdt", Type: mdb.SettingTypeString},
		{Group: mdb.SettingGroupEpay, Key: mdb.SettingKeyEpayDefaultNetwork, Value: "tron", Type: mdb.SettingTypeString},
	} {
		if err := Mdb.Create(&row).Error; err != nil {
			t.Fatalf("precreate %s: %v", row.Key, err)
		}
	}

	seedDefaultSettings()

	for key, want := range map[string]string{
		mdb.SettingKeyEpayDefaultToken:   "usdt",
		mdb.SettingKeyEpayDefaultNetwork: "tron",
	} {
		var row mdb.Setting
		if err := Mdb.Where("`key` = ?", key).Take(&row).Error; err != nil {
			t.Fatalf("load %s seed: %v", key, err)
		}
		if row.Value != want {
			t.Fatalf("%s value = %q, want existing %q", key, row.Value, want)
		}
	}
}

func TestSeedDefaultSettingsDoesNotOverwriteSystemLogLevel(t *testing.T) {
	db := setupSeedSettingsTestDB(t)
	Mdb = db
	if err := Mdb.Create(&mdb.Setting{
		Group: mdb.SettingGroupSystem,
		Key:   mdb.SettingKeySystemLogLevel,
		Value: "debug",
		Type:  mdb.SettingTypeString,
	}).Error; err != nil {
		t.Fatalf("precreate system.log_level: %v", err)
	}

	seedDefaultSettings()

	var row mdb.Setting
	if err := Mdb.Where("`key` = ?", mdb.SettingKeySystemLogLevel).Take(&row).Error; err != nil {
		t.Fatalf("load system.log_level seed: %v", err)
	}
	if row.Value != "debug" {
		t.Fatalf("system.log_level value = %q, want existing debug", row.Value)
	}
}

func setupSeedSettingsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := Mdb
	viper.Reset()
	viper.Set("app_uri", "https://example.com")
	t.Cleanup(func() {
		Mdb = oldDB
		viper.Reset()
	})

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "seed-settings.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&mdb.Setting{}); err != nil {
		t.Fatalf("migrate settings: %v", err)
	}
	return db
}

func setupSeedTableTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	oldDB := Mdb
	t.Cleanup(func() {
		Mdb = oldDB
	})
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "seed-table.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate seed table: %v", err)
	}
	return db
}
