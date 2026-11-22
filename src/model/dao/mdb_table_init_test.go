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

func TestXLayerUsesProviderSettingsOnly(t *testing.T) {
	Mdb = setupSeedTableTestDB(t, &mdb.Chain{}, &mdb.ChainToken{}, &mdb.Setting{})
	seedChains()
	seedChainTokens()
	seedDefaultSettings()
	for _, table := range []interface{}{&mdb.Chain{}, &mdb.ChainToken{}} {
		var count int64
		if err := Mdb.Model(table).Where("network = ?", mdb.NetworkXLayer).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("unexpected X Layer rows in %T: count=%d err=%v", table, count, err)
		}
	}
	var row mdb.Setting
	if err := Mdb.Where(&mdb.Setting{Group: mdb.SettingGroupXLayer, Key: mdb.SettingKeyXLayerAllowTokens}).Take(&row).Error; err != nil || row.Value != "USDC,USDT0,USDG" {
		t.Fatalf("default OKX token list=%q err=%v", row.Value, err)
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
	var strict mdb.Setting
	if err := Mdb.Where(&mdb.Setting{Key: mdb.SettingKeyStrictPayment}).Take(&strict).Error; err != nil || strict.Value != "true" || strict.Type != mdb.SettingTypeBool {
		t.Fatal(strict, err)
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
