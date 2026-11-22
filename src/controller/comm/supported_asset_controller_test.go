package comm

import (
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestBuildSupportedAssetsIncludesConfiguredAptosTokens(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.Chain{Network: mdb.NetworkAptos, Enabled: true, DisplayName: "Aptos"}).Error; err != nil {
		t.Fatalf("create aptos chain: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkAptos, "0x1"); err != nil {
		t.Fatalf("add aptos wallet: %v", err)
	}
	for _, row := range []mdb.ChainToken{
		{Network: mdb.NetworkAptos, Symbol: "USDT", ContractAddress: "0x357b0b74bc833e95a115ad22604854d6b0fca151cecd94111770e5d6ffc9dc2b", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkAptos, Symbol: "MOVEUSD", ContractAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkAptos, Symbol: "USDC", ContractAddress: "", Decimals: 6, Enabled: true},
	} {
		if err := dao.Mdb.Create(&row).Error; err != nil {
			t.Fatalf("create token %s: %v", row.Symbol, err)
		}
	}

	supports, err := buildSupportedAssets(nil)
	if err != nil {
		t.Fatalf("buildSupportedAssets: %v", err)
	}
	if len(supports) != 1 {
		t.Fatalf("supported assets len = %d, want 1: %#v", len(supports), supports)
	}
	if supports[0].Network != mdb.NetworkAptos {
		t.Fatalf("network = %q, want %q", supports[0].Network, mdb.NetworkAptos)
	}
	want := []string{"MOVEUSD", "USDT"}
	if len(supports[0].Tokens) != len(want) {
		t.Fatalf("tokens = %#v, want %#v", supports[0].Tokens, want)
	}
	for i := range want {
		if supports[0].Tokens[i] != want[i] {
			t.Fatalf("tokens = %#v, want %#v", supports[0].Tokens, want)
		}
	}
}

func TestHostedXLayerExcludedFromChainPaymentOptions(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	if err := dao.Mdb.Create(&mdb.Chain{Network: mdb.NetworkXLayer, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Create(&mdb.ChainToken{Network: mdb.NetworkXLayer, Symbol: "USDC", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkXLayer, "0xddd0000000000000000000000000000000000009"); err != nil {
		t.Fatal(err)
	}
	if err := data.SetSetting(mdb.SettingGroupXLayer, mdb.SettingKeyXLayerEnabled, "true", mdb.SettingTypeBool); err != nil {
		t.Fatal(err)
	}
	supports, err := buildSupportedAssets(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range supports {
		if asset.Network == mdb.NetworkXLayer {
			t.Fatalf("OKX listed under chain payments: %+v", asset)
		}
	}
}
