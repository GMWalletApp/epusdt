package dao

import (
	"path/filepath"
	"testing"

	"github.com/GMWalletApp/epusdt/model/mdb"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestSeedTonRowsAreIdempotent confirms two properties that together
// keep the seed safe across restarts:
//  1. seedChains / seedChainTokens / seedRpcNodes actually insert the
//     TON rows we added.
//  2. Re-running each seed does not duplicate any row, so admin edits
//     after first boot are not overwritten on subsequent boots.
func TestSeedTonRowsAreIdempotent(t *testing.T) {
	prev := Mdb
	t.Cleanup(func() { Mdb = prev })

	dbPath := filepath.Join(t.TempDir(), "seed.db")
	db, err := openDB(dbPath, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, m := range []interface{}{&mdb.Chain{}, &mdb.ChainToken{}, &mdb.RpcNode{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate %T: %v", m, err)
		}
	}
	Mdb = db

	seedChains()
	seedChainTokens()
	seedRpcNodes()

	var chainCount int64
	if err := db.Model(&mdb.Chain{}).Where("network = ?", mdb.NetworkTon).Count(&chainCount).Error; err != nil {
		t.Fatalf("count chains: %v", err)
	}
	if chainCount != 1 {
		t.Fatalf("TON chains row count = %d, want 1", chainCount)
	}

	var tokenCount int64
	if err := db.Model(&mdb.ChainToken{}).Where("network = ?", mdb.NetworkTon).Count(&tokenCount).Error; err != nil {
		t.Fatalf("count chain_tokens: %v", err)
	}
	// USDT Jetton + native TON.
	if tokenCount != 2 {
		t.Fatalf("TON chain_tokens count = %d, want 2", tokenCount)
	}

	var rpcCount int64
	if err := db.Model(&mdb.RpcNode{}).Where("network = ?", mdb.NetworkTon).Count(&rpcCount).Error; err != nil {
		t.Fatalf("count rpc_nodes: %v", err)
	}
	if rpcCount != 1 {
		t.Fatalf("TON rpc_nodes count = %d, want 1", rpcCount)
	}

	// Second pass — must be a no-op.
	seedChains()
	seedChainTokens()
	seedRpcNodes()

	for label, q := range map[string]*gorm.DB{
		"chains":       db.Model(&mdb.Chain{}).Where("network = ?", mdb.NetworkTon),
		"chain_tokens": db.Model(&mdb.ChainToken{}).Where("network = ?", mdb.NetworkTon),
		"rpc_nodes":    db.Model(&mdb.RpcNode{}).Where("network = ?", mdb.NetworkTon),
	} {
		var n int64
		if err := q.Count(&n).Error; err != nil {
			t.Fatalf("recount %s: %v", label, err)
		}
		expect := map[string]int64{
			"chains":       1,
			"chain_tokens": 2,
			"rpc_nodes":    1,
		}[label]
		if n != expect {
			t.Fatalf("after second seed, %s count = %d, want %d", label, n, expect)
		}
	}
}
