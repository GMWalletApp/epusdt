package data

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/xssnick/tonutils-go/address"
)

func TestEvmTransactionLockAddressIsCaseInsensitive(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	tradeID := "trade-evm-case"
	address := "0xA1B2c3D4e5F60718293aBcDeF001122334455667"

	if err := LockTransaction("Ethereum", address, "usdt", tradeID, 1.23, time.Hour); err != nil {
		t.Fatalf("lock transaction: %v", err)
	}

	gotTradeID, err := GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkEthereum, strings.ToLower(address), "USDT", 1.23)
	if err != nil {
		t.Fatalf("lookup transaction lock: %v", err)
	}
	if gotTradeID != tradeID {
		t.Fatalf("trade id = %q, want %q", gotTradeID, tradeID)
	}

	if err := UnLockTransaction(mdb.NetworkEthereum, strings.ToUpper(address), "USDT", 1.23); err != nil {
		t.Fatalf("unlock transaction: %v", err)
	}

	gotTradeID, err = GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkEthereum, address, "USDT", 1.23)
	if err != nil {
		t.Fatalf("lookup after unlock: %v", err)
	}
	if gotTradeID != "" {
		t.Fatalf("expected lock to be released, got trade id %q", gotTradeID)
	}
}

func TestStatsBucketExprForDialect(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
		hourly  bool
		want    string
	}{
		{
			name:    "sqlite daily",
			dialect: "sqlite",
			want:    "substr(created_at, 1, 10)",
		},
		{
			name:    "sqlite hourly",
			dialect: "sqlite",
			hourly:  true,
			want:    "replace(substr(created_at, 1, 13), 'T', ' ') || ':00'",
		},
		{
			name:    "postgres daily",
			dialect: "postgres",
			want:    "TO_CHAR(created_at, 'YYYY-MM-DD')",
		},
		{
			name:    "postgres hourly",
			dialect: "postgres",
			hourly:  true,
			want:    "TO_CHAR(created_at, 'YYYY-MM-DD HH24:00')",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := statsBucketExprForDialect(tt.dialect, "created_at", tt.hourly)
			if err != nil {
				t.Fatalf("statsBucketExprForDialect error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("bucket expr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatsBucketExprRejectsUnsupportedDialect(t *testing.T) {
	if _, err := statsBucketExprForDialect("mysql", "created_at", false); err == nil {
		t.Fatal("expected unsupported mysql dialect error")
	}
}

func TestTransactionLockPrecisionPreventsEquivalentAmountsOnly(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "2", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set precision 2: %v", err)
	}
	if err := LockTransaction(mdb.NetworkTron, "TPrecisionAddress001", "USDT", "trade-old", 1.23, time.Hour); err != nil {
		t.Fatalf("lock old transaction: %v", err)
	}

	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "4", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set precision 4: %v", err)
	}
	if err := LockTransaction(mdb.NetworkTron, "TPrecisionAddress001", "USDT", "trade-equivalent", 1.2300, time.Hour); !errors.Is(err, ErrTransactionLocked) {
		t.Fatalf("equivalent lock error = %v, want %v", err, ErrTransactionLocked)
	}
	if err := LockTransaction(mdb.NetworkTron, "TPrecisionAddress001", "USDT", "trade-new", 1.2301, time.Hour); err != nil {
		t.Fatalf("distinct precision lock: %v", err)
	}
}

func TestTransactionLockLookupUsesStoredPrecision(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "4", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set precision 4: %v", err)
	}
	if err := LockTransaction(mdb.NetworkTron, "TPrecisionAddress002", "USDT", "trade-precise", 1.2345, time.Hour); err != nil {
		t.Fatalf("lock precise transaction: %v", err)
	}
	if err := SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyAmountPrecision, "2", mdb.SettingTypeInt); err != nil {
		t.Fatalf("set precision 2: %v", err)
	}

	gotTradeID, err := GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, "TPrecisionAddress002", "USDT", 1.2345)
	if err != nil {
		t.Fatalf("lookup transaction lock: %v", err)
	}
	if gotTradeID != "trade-precise" {
		t.Fatalf("trade id = %q, want trade-precise", gotTradeID)
	}
}

func TestNonEvmTransactionLockAddressRemainsCaseSensitive(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	tradeID := "trade-tron-case"
	address := "TCaseSensitiveAddress001"

	if err := LockTransaction(mdb.NetworkTron, address, "USDT", tradeID, 1.00, time.Hour); err != nil {
		t.Fatalf("lock transaction: %v", err)
	}

	gotTradeID, err := GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTron, strings.ToLower(address), "USDT", 1.00)
	if err != nil {
		t.Fatalf("lookup transaction lock: %v", err)
	}
	if gotTradeID != "" {
		t.Fatalf("tron address lookup should remain case-sensitive, got trade id %q", gotTradeID)
	}
}

func TestTonTransactionLockAddressUsesRawKey(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	bounceable := "EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I"
	addr := address.MustParseAddr(bounceable)
	nonBounce := addr.Bounce(false).String()

	if err := LockTransaction(mdb.NetworkTon, bounceable, "TON", "trade-ton", 1.23, time.Hour); err != nil {
		t.Fatalf("lock ton transaction: %v", err)
	}
	gotTradeID, err := GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, nonBounce, "TON", 1.23)
	if err != nil {
		t.Fatalf("lookup ton lock: %v", err)
	}
	if gotTradeID != "trade-ton" {
		t.Fatalf("ton lock lookup = %q, want trade-ton", gotTradeID)
	}
	gotTradeID, err = GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, addr.StringRaw(), "TON", 1.23)
	if err != nil {
		t.Fatalf("lookup ton raw lock: %v", err)
	}
	if gotTradeID != "trade-ton" {
		t.Fatalf("ton raw lock lookup = %q, want trade-ton", gotTradeID)
	}
}

func TestAptosTransactionLockAddressUsesCanonicalKey(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := LockTransaction(mdb.NetworkAptos, "0xA", "USDT", "trade-aptos", 1.23, time.Hour); err != nil {
		t.Fatalf("lock aptos transaction: %v", err)
	}
	gotTradeID, err := GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkAptos, "a", "USDT", 1.23)
	if err != nil {
		t.Fatalf("lookup aptos lock: %v", err)
	}
	if gotTradeID != "trade-aptos" {
		t.Fatalf("aptos lock lookup = %q, want trade-aptos", gotTradeID)
	}
}
