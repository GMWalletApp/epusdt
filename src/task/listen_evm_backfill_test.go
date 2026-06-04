package task

import (
	"context"
	"reflect"
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

func TestEvmBackfillCursorRoundTrip(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings(): %v", err)
	}

	if err := saveEvmBackfillCursor(mdb.NetworkEthereum, 12345); err != nil {
		t.Fatalf("saveEvmBackfillCursor(): %v", err)
	}

	got, ok := loadEvmBackfillCursor(mdb.NetworkEthereum)
	if !ok {
		t.Fatal("loadEvmBackfillCursor() ok=false, want true")
	}
	if got != 12345 {
		t.Fatalf("loadEvmBackfillCursor() = %d, want 12345", got)
	}
}

func TestBackfillEvmLogsUsesSavedCursorAndPersistsLatest(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings(): %v", err)
	}

	oldBlockNumber := evmBlockNumber
	oldFilterLogs := evmFilterLogs
	oldChunkSize := evmBackfillChunkSize
	t.Cleanup(func() {
		evmBlockNumber = oldBlockNumber
		evmFilterLogs = oldFilterLogs
		evmBackfillChunkSize = oldChunkSize
	})

	evmBackfillChunkSize = 5
	evmBlockNumber = func(context.Context, *ethclient.Client) (uint64, error) {
		return 20, nil
	}

	var ranges [][2]uint64
	evmFilterLogs = func(_ context.Context, _ *ethclient.Client, query ethereum.FilterQuery) ([]types.Log, error) {
		from := query.FromBlock.Uint64()
		to := query.ToBlock.Uint64()
		ranges = append(ranges, [2]uint64{from, to})
		switch {
		case from == 10 && to == 14:
			return []types.Log{
				{BlockNumber: 12, TxIndex: 1, Index: 0},
				{BlockNumber: 10, TxIndex: 2, Index: 0},
			}, nil
		case from == 15 && to == 19:
			return []types.Log{
				{BlockNumber: 17, TxIndex: 0, Index: 1},
			}, nil
		case from == 20 && to == 20:
			return []types.Log{
				{BlockNumber: 20, TxIndex: 0, Index: 0},
			}, nil
		default:
			return nil, nil
		}
	}

	if err := saveEvmBackfillCursor(mdb.NetworkEthereum, 10); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}

	var handled []uint64
	head, err := backfillEvmLogs(context.Background(), nil, "[TEST]", mdb.NetworkEthereum, ethereum.FilterQuery{}, func(_ *ethclient.Client, vLog types.Log) {
		handled = append(handled, vLog.BlockNumber)
	})
	if err != nil {
		t.Fatalf("backfillEvmLogs(): %v", err)
	}
	if head != 20 {
		t.Fatalf("backfillEvmLogs() head = %d, want 20", head)
	}

	wantRanges := [][2]uint64{{10, 14}, {15, 19}, {20, 20}}
	if !reflect.DeepEqual(ranges, wantRanges) {
		t.Fatalf("backfill ranges = %#v, want %#v", ranges, wantRanges)
	}

	wantHandled := []uint64{10, 12, 17, 20}
	if !reflect.DeepEqual(handled, wantHandled) {
		t.Fatalf("handled blocks = %#v, want %#v", handled, wantHandled)
	}

	cursor, ok := loadEvmBackfillCursor(mdb.NetworkEthereum)
	if !ok {
		t.Fatal("loadEvmBackfillCursor() ok=false, want true")
	}
	if cursor != 20 {
		t.Fatalf("cursor = %d, want 20", cursor)
	}
}

func TestBackfillEvmLogsUsesBootstrapLookbackWithoutCursor(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings(): %v", err)
	}

	oldBlockNumber := evmBlockNumber
	oldFilterLogs := evmFilterLogs
	oldChunkSize := evmBackfillChunkSize
	oldLookback := evmBackfillBootstrapLookback
	t.Cleanup(func() {
		evmBlockNumber = oldBlockNumber
		evmFilterLogs = oldFilterLogs
		evmBackfillChunkSize = oldChunkSize
		evmBackfillBootstrapLookback = oldLookback
	})

	evmBackfillChunkSize = 100
	evmBackfillBootstrapLookback = 4
	evmBlockNumber = func(context.Context, *ethclient.Client) (uint64, error) {
		return 12, nil
	}

	var ranges [][2]uint64
	evmFilterLogs = func(_ context.Context, _ *ethclient.Client, query ethereum.FilterQuery) ([]types.Log, error) {
		ranges = append(ranges, [2]uint64{query.FromBlock.Uint64(), query.ToBlock.Uint64()})
		return nil, nil
	}

	head, err := backfillEvmLogs(context.Background(), nil, "[TEST]", mdb.NetworkPolygon, ethereum.FilterQuery{}, func(_ *ethclient.Client, _ types.Log) {})
	if err != nil {
		t.Fatalf("backfillEvmLogs(): %v", err)
	}
	if head != 12 {
		t.Fatalf("backfillEvmLogs() head = %d, want 12", head)
	}

	wantRanges := [][2]uint64{{9, 12}}
	if !reflect.DeepEqual(ranges, wantRanges) {
		t.Fatalf("bootstrap ranges = %#v, want %#v", ranges, wantRanges)
	}

	cursor, ok := loadEvmBackfillCursor(mdb.NetworkPolygon)
	if !ok {
		t.Fatal("loadEvmBackfillCursor() ok=false, want true")
	}
	if cursor != 12 {
		t.Fatalf("cursor = %d, want 12", cursor)
	}
}
