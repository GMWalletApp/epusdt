package service

import (
	"testing"
	"time"

	"github.com/xssnick/tonutils-go/tvm/cell"
)

// Pins the classifier that decides whether an internal message is a
// wallet-to-wallet payment or a contract notification. Permissive on
// edge cases (nil/short body) because dropping a real transfer is
// worse than accepting a contract notification we then can't match.
func TestIsPlainTonTransferBodyClassifiesMessageShapes(t *testing.T) {
	cases := []struct {
		name string
		body *cell.Cell
		want bool
	}{
		{
			name: "nil body is a plain transfer (no comment)",
			body: nil,
			want: true,
		},
		{
			name: "empty body cell is a plain transfer",
			body: cell.BeginCell().EndCell(),
			want: true,
		},
		{
			name: "body shorter than 32 bits is treated as plain (defensive)",
			body: cell.BeginCell().MustStoreUInt(0xff, 8).EndCell(),
			want: true,
		},
		{
			name: "opcode 0 (text comment) is a plain transfer",
			body: cell.BeginCell().MustStoreUInt(tonOpTextComment, 32).MustStoreStringSnake("thanks for the coffee").EndCell(),
			want: true,
		},
		{
			name: "encrypted comment opcode 0x2167da4b is a plain transfer",
			body: cell.BeginCell().MustStoreUInt(tonOpEncryptedComment, 32).MustStoreUInt(0xabcdef, 32).EndCell(),
			want: true,
		},
		{
			name: "non-zero opcode is a contract notification (rejected)",
			body: cell.BeginCell().MustStoreUInt(0x7362d09c, 32).EndCell(), // jetton transfer_notification
			want: false,
		},
		{
			name: "another non-zero opcode (NFT ownership_assigned) is rejected",
			body: cell.BeginCell().MustStoreUInt(0x05138d91, 32).EndCell(),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPlainTonTransferBody(tc.body); got != tc.want {
				t.Fatalf("isPlainTonTransferBody = %v, want %v", got, tc.want)
			}
		})
	}
}

func resetTonPendingConfirm(t *testing.T) {
	t.Helper()
	gTonPendingConfirm.Range(func(k, _ interface{}) bool {
		gTonPendingConfirm.Delete(k)
		return true
	})
}

// Pins the safety property: a tx settles only after master seqno has
// actually advanced by min_confirmations from first sight. Wall-clock
// age never grants depth (which would over-credit on slow blocks or
// clock skew).
func TestTonConfirmationDepthReachedRequiresBlockAdvance(t *testing.T) {
	resetTonPendingConfirm(t)

	// minConfirmations <= 0 disables the gate entirely.
	if !tonConfirmationDepthReached("any", 1000, 0) {
		t.Fatal("min=0 should bypass the gate")
	}
	if !tonConfirmationDepthReached("any", 1000, -5) {
		t.Fatal("negative min should bypass the gate")
	}

	// Fresh tx: first observation defers, no head-start possible.
	resetTonPendingConfirm(t)
	if tonConfirmationDepthReached("tx-A", 100, 1) {
		t.Fatal("first observation should defer")
	}
	// Same seqno on second look — still defers.
	if tonConfirmationDepthReached("tx-A", 100, 1) {
		t.Fatal("no master advance should keep deferring")
	}
	// Master advanced by 1 → depth=1 ≥ min=1, allow.
	if !tonConfirmationDepthReached("tx-A", 101, 1) {
		t.Fatal("depth=1 should satisfy min=1")
	}

	// New tx, min=3 — needs 3 master advances after first sight.
	resetTonPendingConfirm(t)
	if tonConfirmationDepthReached("tx-B", 500, 3) {
		t.Fatal("first observation defers")
	}
	if tonConfirmationDepthReached("tx-B", 501, 3) {
		t.Fatal("depth=1 < min=3 should defer")
	}
	if tonConfirmationDepthReached("tx-B", 502, 3) {
		t.Fatal("depth=2 < min=3 should defer")
	}
	if !tonConfirmationDepthReached("tx-B", 503, 3) {
		t.Fatal("depth=3 should satisfy min=3")
	}
}

// Pins no-over-credit: first call MUST return false regardless of
// currentSeqno or min. Depth can only come from observed master
// advances.
func TestTonConfirmationDepthReachedDefersFirstSightAlways(t *testing.T) {
	resetTonPendingConfirm(t)
	if tonConfirmationDepthReached("never-seen-before", 10_000_000, 1) {
		t.Fatal("first sight must defer regardless of current seqno or min")
	}
	// Stored entry should anchor at the current seqno (no head-start).
	v, ok := gTonPendingConfirm.Load("never-seen-before")
	if !ok {
		t.Fatal("first sight should have stored a pending entry")
	}
	if got := v.(tonPendingEntry).firstSeenMasterSeqno; got != 10_000_000 {
		t.Fatalf("anchor should equal currentMasterSeqno; got %d", got)
	}
}

// TTL has a 1h floor for small min_confirmations and scales linearly
// above that so large admin values get time to satisfy.
func TestTonPendingTTLSecondsScalesAndFloors(t *testing.T) {
	cases := []struct {
		minConfirmations int
		wantMin          int64
	}{
		{minConfirmations: 0, wantMin: tonPendingFloorTTL},
		{minConfirmations: 1, wantMin: tonPendingFloorTTL},
		{minConfirmations: 100, wantMin: tonPendingFloorTTL},
		// 1000 blocks * 5s * 3 = 15000s > 3600s floor.
		{minConfirmations: 1000, wantMin: 15000},
		// Very high values must scale, not get capped.
		{minConfirmations: 10000, wantMin: 150000},
	}
	for _, c := range cases {
		got := tonPendingTTLSeconds(c.minConfirmations)
		if got < c.wantMin {
			t.Fatalf("min=%d ttl=%d should be >= %d", c.minConfirmations, got, c.wantMin)
		}
	}
}

// Misconfigured min_confirmations clamps to a supported range:
// <=0 disables, oversized values clamp down (not silently broken).
func TestEffectiveTonMinConfirmationsClamps(t *testing.T) {
	if got := effectiveTonMinConfirmations(0); got != 0 {
		t.Fatalf("0 should disable gate (got %d)", got)
	}
	if got := effectiveTonMinConfirmations(-3); got != 0 {
		t.Fatalf("negative should disable gate (got %d)", got)
	}
	if got := effectiveTonMinConfirmations(1); got != 1 {
		t.Fatalf("normal value passes through (got %d)", got)
	}
	if got := effectiveTonMinConfirmations(tonMaxEffectiveMinConfirmations); got != tonMaxEffectiveMinConfirmations {
		t.Fatalf("max boundary should pass (got %d)", got)
	}
	if got := effectiveTonMinConfirmations(tonMaxEffectiveMinConfirmations + 1); got != tonMaxEffectiveMinConfirmations {
		t.Fatalf("above max should clamp to max (got %d)", got)
	}
	if got := effectiveTonMinConfirmations(10_000_000); got != tonMaxEffectiveMinConfirmations {
		t.Fatalf("very large should clamp to max (got %d)", got)
	}
}

// Per-entry expiry sweep: expired entries go, long-TTL ones survive
// past the old 1h global policy.
func TestCleanupTonPendingConfirmEvictsExpiredEntries(t *testing.T) {
	resetTonPendingConfirm(t)
	now := time.Now().Unix()

	gTonPendingConfirm.Store("expired", tonPendingEntry{firstSeenMasterSeqno: 1, expiresAt: now - 60})
	gTonPendingConfirm.Store("active", tonPendingEntry{firstSeenMasterSeqno: 1, expiresAt: now + 3600})
	gTonPendingConfirm.Store("long-min-conf", tonPendingEntry{firstSeenMasterSeqno: 1, expiresAt: now + 24*3600})

	cleanupTonPendingConfirm()

	if _, ok := gTonPendingConfirm.Load("expired"); ok {
		t.Fatal("expired entry should have been evicted")
	}
	if _, ok := gTonPendingConfirm.Load("active"); !ok {
		t.Fatal("active entry should survive")
	}
	if _, ok := gTonPendingConfirm.Load("long-min-conf"); !ok {
		t.Fatal("long-TTL entry should survive past the old 1h policy")
	}
}
