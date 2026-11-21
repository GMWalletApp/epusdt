package data

import (
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// GetXLayerToAddress must prefer the wallet_address table (the single source
// of truth since the admin-integration change, XLAYER-ADMIN-INTEGRATION-DESIGN.md)
// and only fall back to the legacy settings key while no enabled wallet row
// exists. With several enabled rows the oldest one wins, deterministically.
func TestGetXLayerToAddressPrefersWalletTable(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const legacy = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := SetSetting(mdb.SettingGroupXLayer, mdb.SettingKeyXLayerToAddress, legacy, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed legacy to_address: %v", err)
	}

	// No wallet row yet -> legacy fallback.
	if got := GetXLayerToAddress(); got != legacy {
		t.Fatalf("GetXLayerToAddress() = %q, want legacy fallback %q", got, legacy)
	}

	const oldest = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const newest = "0xcccccccccccccccccccccccccccccccccccccccc"
	for _, addr := range []string{oldest, newest} {
		if _, err := AddWalletAddressWithNetwork(mdb.NetworkXLayer, addr); err != nil {
			t.Fatalf("add wallet %s: %v", addr, err)
		}
	}
	if got := GetXLayerToAddress(); got != oldest {
		t.Fatalf("GetXLayerToAddress() = %q, want oldest enabled wallet %q", got, oldest)
	}

	// Disabling the wallet removes it from consideration.
	row, err := GetWalletAddressByNetworkAndAddress(mdb.NetworkXLayer, oldest)
	if err != nil {
		t.Fatalf("load wallet: %v", err)
	}
	if err := ChangeWalletAddressStatus(row.ID, mdb.TokenStatusDisable); err != nil {
		t.Fatalf("disable wallet: %v", err)
	}
	if got := GetXLayerToAddress(); got != newest {
		t.Fatalf("GetXLayerToAddress() = %q after disabling oldest, want %q", got, newest)
	}
}

// GetXLayerAllowTokens must be driven by the chain_tokens table once xlayer
// rows exist (the per-token switches on the admin chains page write there);
// the legacy settings key only applies while the table has no xlayer rows.
// An all-disabled table intentionally yields an empty list.
func TestGetXLayerAllowTokensPrefersChainTokens(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const legacy = "USDC,USDT0,USDG"
	if err := SetSetting(mdb.SettingGroupXLayer, mdb.SettingKeyXLayerAllowTokens, legacy, mdb.SettingTypeString); err != nil {
		t.Fatalf("seed legacy allow_tokens: %v", err)
	}

	// No chain_tokens rows -> legacy fallback.
	got := GetXLayerAllowTokens()
	if len(got) != 3 {
		t.Fatalf("GetXLayerAllowTokens() = %#v, want the 3 legacy tokens", got)
	}

	rows := []mdb.ChainToken{
		{Network: mdb.NetworkXLayer, Symbol: "USDC", ContractAddress: "0xb6ceceab302e2e4948951ee7843fc24e92933061", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkXLayer, Symbol: "USDT0", ContractAddress: "0x779ded0c9e1022225f8e0630b35a9b54be713736", Decimals: 6, Enabled: true},
		{Network: mdb.NetworkXLayer, Symbol: "USDG", ContractAddress: "0x4ae46a509f6b1d9056937ba4500cb143933d2dc8", Decimals: 6, Enabled: true},
	}
	for i := range rows {
		if err := dao.Mdb.Create(&rows[i]).Error; err != nil {
			t.Fatalf("create token row %s: %v", rows[i].Symbol, err)
		}
	}
	// Enabled has a gorm default:true tag, so a plain Create with false would
	// still store true — disable explicitly after insert.
	if err := dao.Mdb.Model(&mdb.ChainToken{}).
		Where("network = ? AND symbol IN ?", mdb.NetworkXLayer, []string{"USDT0", "USDG"}).
		Update("enabled", false).Error; err != nil {
		t.Fatalf("disable USDT0/USDG rows: %v", err)
	}

	got = GetXLayerAllowTokens()
	if len(got) != 1 || got[0] != "USDC" {
		t.Fatalf("GetXLayerAllowTokens() = %#v, want [USDC] (only enabled row)", got)
	}

	// All disabled -> empty list, not the legacy fallback.
	if err := dao.Mdb.Model(&mdb.ChainToken{}).Where("network = ?", mdb.NetworkXLayer).
		Update("enabled", false).Error; err != nil {
		t.Fatalf("disable tokens: %v", err)
	}
	if got := GetXLayerAllowTokens(); len(got) != 0 {
		t.Fatalf("GetXLayerAllowTokens() = %#v, want empty when every row is disabled", got)
	}
}
