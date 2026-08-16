package data

import (
	"strings"
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/xssnick/tonutils-go/address"
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

func TestAddWalletAddressWithNetworkNormalizesTonAddressVariants(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	bounceable := "EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I"
	addr := address.MustParseAddr(bounceable)
	expected := addr.Bounce(false).String()

	row, err := AddWalletAddressWithNetwork(mdb.NetworkTon, addr.StringRaw())
	if err != nil {
		t.Fatalf("add raw ton wallet: %v", err)
	}
	if row.Address != expected {
		t.Fatalf("stored TON address = %q, want %q", row.Address, expected)
	}
	if _, err = AddWalletAddressWithNetwork(mdb.NetworkTon, bounceable); err != constant.WalletAddressAlreadyExists {
		t.Fatalf("add equivalent ton wallet error = %v, want already exists", err)
	}
}

func TestAddWalletAddressWithNetworkNormalizesMoveAddressVariants(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	want := "0x000000000000000000000000000000000000000000000000000000000000000a"
	row, err := AddWalletAddressWithNetwork(mdb.NetworkAptos, " A ")
	if err != nil {
		t.Fatalf("add aptos wallet: %v", err)
	}
	if row.Address != want {
		t.Fatalf("stored Aptos address = %q, want %q", row.Address, want)
	}
	if _, err = AddWalletAddressWithNetwork(mdb.NetworkAptos, "0x0A"); err != constant.WalletAddressAlreadyExists {
		t.Fatalf("add equivalent aptos wallet error = %v, want already exists", err)
	}
}

func TestAddedEvmNetworksNormalizeWalletAddresses(t *testing.T) {
	for _, network := range []string{mdb.NetworkBase, mdb.NetworkArbitrum} {
		t.Run(network, func(t *testing.T) {
			cleanup := testutil.SetupTestDatabases(t)
			defer cleanup()
			input := "0xA1B2c3D4e5F60718293aBcDeF001122334455667"
			row, err := AddWalletAddressWithNetwork(network, input)
			if err != nil {
				t.Fatalf("add wallet: %v", err)
			}
			if row.Address != strings.ToLower(input) {
				t.Fatalf("wallet address = %q", row.Address)
			}
		})
	}
}
