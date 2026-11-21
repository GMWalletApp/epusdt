package comm

import (
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/response"
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

	supports, err := buildSupportedAssets()
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

// Since the admin-integration change (XLAYER-ADMIN-INTEGRATION-DESIGN.md) X
// Layer is a real chains/chain_tokens/wallet_address rowset instead of a
// pseudo-network: the cashier entry is produced by the generic path and is
// gated by the chains row, an enabled wallet (the receiving address), the
// per-token switches, the settings master switch, and the Go contract map.
func TestBuildSupportedAssetsXLayerDrivenByChainsAndTokens(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.Chain{Network: mdb.PaymentProviderXLayer, Enabled: true, DisplayName: "X Layer"}).Error; err != nil {
		t.Fatalf("create xlayer chain: %v", err)
	}
	if _, err := data.AddWalletAddressWithNetwork(mdb.PaymentProviderXLayer, "0xddd0000000000000000000000000000000000009"); err != nil {
		t.Fatalf("add xlayer wallet: %v", err)
	}
	tokens := []mdb.ChainToken{
		{Network: mdb.PaymentProviderXLayer, Symbol: "USDC", ContractAddress: "0xb6ceceab302e2e4948951ee7843fc24e92933061", Decimals: 6, Enabled: true},
		{Network: mdb.PaymentProviderXLayer, Symbol: "USDT0", ContractAddress: "0x779ded0c9e1022225f8e0630b35a9b54be713736", Decimals: 6, Enabled: true},
		{Network: mdb.PaymentProviderXLayer, Symbol: "USDG", ContractAddress: "0x4ae46a509f6b1d9056937ba4500cb143933d2dc8", Decimals: 6, Enabled: true},
		// A token the Go contract map does not know can never be created at
		// the provider, so it must not be offered in the cashier.
		{Network: mdb.PaymentProviderXLayer, Symbol: "FOOCOIN", ContractAddress: "0xeeee0000000000000000000000000000000000008", Decimals: 6, Enabled: true},
	}
	for _, row := range tokens {
		if err := dao.Mdb.Create(&row).Error; err != nil {
			t.Fatalf("create token %s: %v", row.Symbol, err)
		}
	}

	findXLayer := func() (response.NetworkTokenSupport, bool) {
		supports, err := buildSupportedAssets()
		if err != nil {
			t.Fatalf("buildSupportedAssets: %v", err)
		}
		for _, s := range supports {
			if s.Network == mdb.PaymentProviderXLayer {
				return s, true
			}
		}
		return response.NetworkTokenSupport{}, false
	}

	// Master switch off -> X Layer must not appear at all.
	if entry, ok := findXLayer(); ok {
		t.Fatalf("xlayer present with master switch off: %#v", entry)
	}
	if err := data.SetSetting(mdb.SettingGroupXLayer, mdb.SettingKeyXLayerEnabled, "true", mdb.SettingTypeBool); err != nil {
		t.Fatalf("enable xlayer: %v", err)
	}

	entry, ok := findXLayer()
	if !ok {
		t.Fatal("xlayer missing with chain+wallet+tokens enabled")
	}
	if entry.DisplayName != "X Layer" {
		t.Fatalf("display_name = %q, want X Layer", entry.DisplayName)
	}
	want := []string{"USDC", "USDG", "USDT0"}
	if len(entry.Tokens) != len(want) {
		t.Fatalf("tokens = %#v, want %#v (FOOCOIN must be filtered)", entry.Tokens, want)
	}
	for i := range want {
		if entry.Tokens[i] != want[i] {
			t.Fatalf("tokens = %#v, want %#v", entry.Tokens, want)
		}
	}

	// Disabling a token row must remove it from the cashier listing.
	var usdt0 mdb.ChainToken
	if err := dao.Mdb.Where("network = ? AND symbol = ?", mdb.PaymentProviderXLayer, "USDT0").Take(&usdt0).Error; err != nil {
		t.Fatalf("load USDT0 row: %v", err)
	}
	if err := dao.Mdb.Model(&mdb.ChainToken{}).Where("id = ?", usdt0.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable USDT0: %v", err)
	}
	entry, ok = findXLayer()
	if !ok {
		t.Fatal("xlayer missing after disabling one token")
	}
	want = []string{"USDC", "USDG"}
	if len(entry.Tokens) != len(want) {
		t.Fatalf("tokens = %#v, want %#v", entry.Tokens, want)
	}
	for i := range want {
		if entry.Tokens[i] != want[i] {
			t.Fatalf("tokens = %#v, want %#v", entry.Tokens, want)
		}
	}

	// Disabling the wallet (receiving address) removes the entry entirely.
	wallet, err := data.GetWalletAddressByNetworkAndAddress(mdb.PaymentProviderXLayer, "0xddd0000000000000000000000000000000000009")
	if err != nil {
		t.Fatalf("load wallet: %v", err)
	}
	if err := data.ChangeWalletAddressStatus(wallet.ID, mdb.TokenStatusDisable); err != nil {
		t.Fatalf("disable wallet: %v", err)
	}
	if entry, ok := findXLayer(); ok {
		t.Fatalf("xlayer present with wallet disabled: %#v", entry)
	}
}
