package data

import (
	"slices"
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestXLayerSettingsSupplyAddressAndAllowedTokens(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	const wallet = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := AddWalletAddressWithNetwork(mdb.NetworkXLayer, wallet); err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Create(&mdb.ChainToken{Network: mdb.NetworkXLayer, Symbol: "USDC", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	set := func(key, value string) {
		t.Helper()
		if err := SetSetting(mdb.SettingGroupXLayer, key, value, mdb.SettingTypeString); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{mdb.SettingKeyXLayerAPIKey, mdb.SettingKeyXLayerAPISecret, mdb.SettingKeyXLayerPassphrase} {
		set(key, "test-value")
	}
	if XLayerConfigured() || GetXLayerToAddress() != "" {
		t.Fatal("must configure an OKX receiving address")
	}
	const recipient = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	set(mdb.SettingKeyXLayerToAddress, recipient)
	if !XLayerConfigured() || GetXLayerToAddress() != recipient {
		t.Fatal("configured OKX address missing")
	}
	set(mdb.SettingKeyXLayerAllowTokens, " USDG,usdt0,USDG,OTHER")
	if got := GetXLayerAllowTokens(); !slices.Equal(got, []string{"USDG", "USDT0"}) {
		t.Fatalf("allowed=%v", got)
	}
	set(mdb.SettingKeyXLayerAllowTokens, "")
	if got := GetXLayerAllowTokens(); len(got) != 0 {
		t.Fatalf("empty selection returned %v", got)
	}
}
