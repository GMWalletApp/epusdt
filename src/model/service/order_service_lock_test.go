package service

import (
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// Symmetric to the listener gate: TON locks outlive the gate's max
// wait by construction. Other networks keep the base TTL.
func TestLockExpirationForNetworkAddsTonBuffer(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	base := 10 * time.Minute
	// SetupTestDatabases sets order_expiration_time=10 via viper.

	// Non-TON network: no buffer.
	if got := lockExpirationForNetwork(mdb.NetworkTron); got != base {
		t.Fatalf("non-TON network should not buffer: got %v want %v", got, base)
	}

	// TON with no chain row in DB (or 0 min_confirmations) falls
	// back to the loader's default of 1, so buffer = 1 * 5s.
	if got := lockExpirationForNetwork(mdb.NetworkTon); got != base+5*time.Second {
		t.Fatalf("TON with default min should add 1*5s: got %v want %v", got, base+5*time.Second)
	}

	// Bump TON's min_confirmations to 3 → buffer = 15s.
	if err := dao.Mdb.Model(&mdb.Chain{}).
		Where("network = ?", mdb.NetworkTon).
		Update("min_confirmations", 3).Error; err != nil {
		t.Fatalf("update min_confirmations: %v", err)
	}
	if got := lockExpirationForNetwork(mdb.NetworkTon); got != base+15*time.Second {
		t.Fatalf("TON min=3 should add 15s: got %v want %v", got, base+15*time.Second)
	}

	// Misconfigured huge value — clamped, NOT 10000*5s.
	if err := dao.Mdb.Model(&mdb.Chain{}).
		Where("network = ?", mdb.NetworkTon).
		Update("min_confirmations", 10_000_000).Error; err != nil {
		t.Fatalf("update min_confirmations: %v", err)
	}
	got := lockExpirationForNetwork(mdb.NetworkTon)
	wantMax := base + time.Duration(tonMaxEffectiveMinConfirmations*tonBlockTimeSeconds)*time.Second
	if got != wantMax {
		t.Fatalf("oversized min_confirmations should clamp: got %v want %v", got, wantMax)
	}
}
