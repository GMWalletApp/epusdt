package data

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
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

	if err := UnLockTransactionByTradeId(tradeID); err != nil {
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

func TestStatsBucketGroupsSQLiteTimestamps(t *testing.T) {
	defer testutil.SetupTestDatabases(t)()
	for _, timestamp := range []string{"2026-01-02 15:04:05", "2026-01-02T15:04:05Z"} {
		for _, tc := range []struct {
			hourly bool
			want   string
		}{{false, "2026-01-02"}, {true, "2026-01-02 15:00"}} {
			var got string
			err := dao.Mdb.Raw("SELECT "+statsBucketExpr(tc.hourly)+" FROM (SELECT ? AS created_at)", timestamp).Scan(&got).Error
			if err != nil || got != tc.want {
				t.Fatalf("timestamp %q hourly=%v: got %q, want %q (error %v)", timestamp, tc.hourly, got, tc.want, err)
			}
		}
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
