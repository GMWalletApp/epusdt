package data

import (
	"strings"
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	tonaddress "github.com/xssnick/tonutils-go/address"
)

func TestAddWalletAddressWithNetworkNormalizesEvmAddressToLowercase(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	input := "0xA1B2c3D4e5F60718293aBcDeF001122334455667"
	row, err := AddWalletAddressWithNetwork(mdb.NetworkEthereum, input)
	if err != nil {
		t.Fatalf("add wallet: %v", err)
	}
	if row.Address != strings.ToLower(input) {
		t.Fatalf("wallet address = %q, want %q", row.Address, strings.ToLower(input))
	}

	loaded, err := GetWalletAddressByNetworkAndAddress(mdb.NetworkEthereum, strings.ToUpper(input))
	if err != nil {
		t.Fatalf("load wallet by mixed-case address: %v", err)
	}
	if loaded.ID == 0 {
		t.Fatal("expected to find wallet by mixed-case query")
	}
	if loaded.Address != strings.ToLower(input) {
		t.Fatalf("stored wallet address = %q, want lowercase", loaded.Address)
	}
}

func TestGetAvailableWalletAddressByNetworkReturnsLowercaseForEvm(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	mixed := "0xA1B2c3D4e5F60718293aBcDeF001122334455667"
	if err := dao.Mdb.Create(&mdb.WalletAddress{
		Network: mdb.NetworkEthereum,
		Address: mixed,
		Status:  mdb.TokenStatusEnable,
	}).Error; err != nil {
		t.Fatalf("seed mixed-case wallet: %v", err)
	}

	rows, err := GetAvailableWalletAddressByNetwork("Ethereum")
	if err != nil {
		t.Fatalf("list wallets: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("wallet count = %d, want 1", len(rows))
	}
	if rows[0].Address != strings.ToLower(mixed) {
		t.Fatalf("listed wallet address = %q, want %q", rows[0].Address, strings.ToLower(mixed))
	}
}

func TestAddWalletAddressWithNetworkKeepsOriginalCaseForNonEvm(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	tronAddress := "TCaseSensitiveTronAddress001"
	tronRow, err := AddWalletAddressWithNetwork(mdb.NetworkTron, tronAddress)
	if err != nil {
		t.Fatalf("add tron wallet: %v", err)
	}
	if tronRow.Address != tronAddress {
		t.Fatalf("tron wallet address = %q, want %q", tronRow.Address, tronAddress)
	}

	solAddress := "SoLAnACaseSensitiveAddress111111111111111111"
	solRow, err := AddWalletAddressWithNetwork(mdb.NetworkSolana, solAddress)
	if err != nil {
		t.Fatalf("add solana wallet: %v", err)
	}
	if solRow.Address != solAddress {
		t.Fatalf("solana wallet address = %q, want %q", solRow.Address, solAddress)
	}
}

// TestNormalizeTonAddressCollapsesSurfaceForms confirms that the three
// user-facing TON address forms — bounceable (EQ…), non-bounceable
// (UQ…), and raw (workchain:hex) — collapse to one canonical storage
// key so a lock written from a notification matches a wallet entered
// from the admin UI.
func TestNormalizeTonAddressCollapsesSurfaceForms(t *testing.T) {
	bounceable := "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs"
	parsed, err := tonaddress.ParseAddr(bounceable)
	if err != nil {
		t.Fatalf("parse seed bounceable: %v", err)
	}
	nonBounceable := parsed.Bounce(false).String()
	raw := parsed.StringRaw()

	canonical := normalizeTonAddress(bounceable)
	if canonical != bounceable {
		t.Fatalf("bounceable input should round-trip, got %q want %q", canonical, bounceable)
	}
	if got := normalizeTonAddress(nonBounceable); got != canonical {
		t.Fatalf("non-bounceable did not normalize to canonical: got %q want %q", got, canonical)
	}
	if got := normalizeTonAddress(raw); got != canonical {
		t.Fatalf("raw form did not normalize to canonical: got %q want %q", got, canonical)
	}
}

func TestAddWalletAddressWithNetworkCanonicalizesTonAddress(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	bounceable := "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs"
	parsed, err := tonaddress.ParseAddr(bounceable)
	if err != nil {
		t.Fatalf("parse seed bounceable: %v", err)
	}
	nonBounceable := parsed.Bounce(false).String()

	row, err := AddWalletAddressWithNetwork(mdb.NetworkTon, nonBounceable)
	if err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}
	if row.Address != bounceable {
		t.Fatalf("stored TON address = %q, want canonical %q", row.Address, bounceable)
	}

	// Looking up the same wallet by either surface form must hit the row.
	loaded, err := GetWalletAddressByNetworkAndAddress(mdb.NetworkTon, bounceable)
	if err != nil {
		t.Fatalf("load by bounceable: %v", err)
	}
	if loaded.ID == 0 {
		t.Fatal("expected to find TON wallet by bounceable form")
	}
}
