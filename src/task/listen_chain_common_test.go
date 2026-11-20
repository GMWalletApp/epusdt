package task

import (
	"math/big"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	epLog "github.com/GMWalletApp/epusdt/util/log"
	"github.com/ethereum/go-ethereum/core/types"
	"go.uber.org/zap"
)

func init() {
	epLog.Sugar = zap.NewNop().Sugar()
}

func TestResolveChainWsURLRequiresEnabledRpcNode(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if got, ok := resolveChainWsURL(mdb.NetworkEthereum, "[TEST]"); ok {
		t.Fatalf("resolveChainWsURL() = (%q, true), want false", got)
	}
}

func TestResolveChainWsURLWithRow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	node := &mdb.RpcNode{
		Network: mdb.NetworkEthereum,
		Url:     " wss://ethereum.example.com ",
		Type:    mdb.RpcNodeTypeWs,
		Weight:  1,
		Enabled: true,
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(node).Error; err != nil {
		t.Fatalf("seed rpc_node: %v", err)
	}

	got, ok := resolveChainWsURL(mdb.NetworkEthereum, "[TEST]")
	if !ok {
		t.Fatalf("resolveChainWsURL() ok=false, want true")
	}
	if got != "wss://ethereum.example.com" {
		t.Fatalf("resolveChainWsURL() = %q, want wss://ethereum.example.com", got)
	}
}

func TestResolveChainWsURLIgnoresManualVerifyOnly(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Create(&mdb.RpcNode{
		Network: mdb.NetworkEthereum,
		Url:     "wss://paid-ethereum.example.com",
		Type:    mdb.RpcNodeTypeWs,
		Weight:  100,
		Enabled: true,
		Purpose: mdb.RpcNodePurposeManualVerify,
		Status:  mdb.RpcNodeStatusOk,
	}).Error; err != nil {
		t.Fatalf("seed manual rpc_node: %v", err)
	}

	if got, ok := resolveChainWsURL(mdb.NetworkEthereum, "[TEST]"); ok {
		t.Fatalf("resolveChainWsURL() = (%q, true), want false", got)
	}
}

func TestResolveChainWsURLUsesGeneralWhenManualVerifyExists(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	rows := []mdb.RpcNode{
		{Network: mdb.NetworkEthereum, Url: "wss://paid-ethereum.example.com", Type: mdb.RpcNodeTypeWs, Weight: 100, Enabled: true, Purpose: mdb.RpcNodePurposeManualVerify, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkEthereum, Url: " wss://general-ethereum.example.com ", Type: mdb.RpcNodeTypeWs, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
	}
	if err := dao.Mdb.Create(&rows).Error; err != nil {
		t.Fatalf("seed rpc_nodes: %v", err)
	}

	got, ok := resolveChainWsURL(mdb.NetworkEthereum, "[TEST]")
	if !ok {
		t.Fatalf("resolveChainWsURL() ok=false, want true")
	}
	if got != "wss://general-ethereum.example.com" {
		t.Fatalf("resolveChainWsURL() = %q, want general node", got)
	}
}

func TestResolveChainWsNodeSkipsCoolingNode(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)

	rows := []mdb.RpcNode{
		{Network: mdb.NetworkEthereum, Url: "wss://primary-ethereum.example.com", Type: mdb.RpcNodeTypeWs, Weight: 100, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkEthereum, Url: "wss://backup-ethereum.example.com", Type: mdb.RpcNodeTypeWs, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
	}
	if err := dao.Mdb.Create(&rows).Error; err != nil {
		t.Fatalf("seed rpc_nodes: %v", err)
	}

	primary, ok := resolveChainWsNode(mdb.NetworkEthereum, "[TEST]")
	if !ok || primary.Url != "wss://primary-ethereum.example.com" {
		t.Fatalf("primary node = %#v ok=%v, want primary", primary, ok)
	}
	for i := 0; i < data.RpcFailoverThreshold; i++ {
		data.RecordRpcNodeFailure(primary.ID)
	}

	got, ok := resolveChainWsNode(mdb.NetworkEthereum, "[TEST]")
	if !ok {
		t.Fatalf("resolveChainWsNode() ok=false, want true")
	}
	if got.Url != "wss://backup-ethereum.example.com" {
		t.Fatalf("resolveChainWsNode() = %#v, want backup", got)
	}
}

func TestEvmWsNodeFailureRecordsRuntimeStats(t *testing.T) {
	data.ResetRpcFailoverForTest()
	data.ResetRpcRuntimeStatsForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)
	t.Cleanup(data.ResetRpcRuntimeStatsForTest)

	recordEvmNodeFailure("[TEST]", mdb.NetworkEthereum, mdb.RpcNode{
		BaseModel: mdb.BaseModel{ID: 123},
		Url:       "wss://ethereum.example.com",
		Type:      mdb.RpcNodeTypeWs,
	}, "dial")

	stats := data.SnapshotRpcRuntimeStats()
	eth := stats[mdb.NetworkEthereum]
	if eth.SuccessCount != 0 {
		t.Fatalf("ethereum success_count = %d, want 0", eth.SuccessCount)
	}
	if eth.FailureCount != 1 {
		t.Fatalf("ethereum failure_count = %d, want 1", eth.FailureCount)
	}
}

func TestEvmLogBlockHeightRecordsRuntimeHeight(t *testing.T) {
	data.ResetRpcRuntimeStatsForTest()
	t.Cleanup(data.ResetRpcRuntimeStatsForTest)

	recordEvmLogBlockHeight(mdb.NetworkEthereum, "[TEST]", 123456)
	recordEvmLogBlockHeight(mdb.NetworkEthereum, "[TEST]", 123455)

	stats := data.SnapshotRpcRuntimeStats()
	eth := stats[mdb.NetworkEthereum]
	if eth.LatestBlockHeight != 123456 {
		t.Fatalf("ethereum latest_block_height = %d, want 123456", eth.LatestBlockHeight)
	}
}

func TestEvmHeaderBlockHeightRecordsRuntimeHeight(t *testing.T) {
	data.ResetRpcRuntimeStatsForTest()
	t.Cleanup(data.ResetRpcRuntimeStatsForTest)

	recordEvmHeaderBlockHeight(mdb.NetworkPolygon, "[TEST]", &types.Header{Number: big.NewInt(7654321)})
	recordEvmHeaderBlockHeight(mdb.NetworkPolygon, "[TEST]", &types.Header{Number: big.NewInt(7654320)})

	stats := data.SnapshotRpcRuntimeStats()
	polygon := stats[mdb.NetworkPolygon]
	if polygon.LatestBlockHeight != 7654321 {
		t.Fatalf("polygon latest_block_height = %d, want 7654321", polygon.LatestBlockHeight)
	}
}

func TestShouldRefreshEvmLatestHeaderAfterIdleInterval(t *testing.T) {
	lastUpdate := time.Now()

	if shouldRefreshEvmLatestHeader(lastUpdate, false, lastUpdate.Add(evmStatsIdleInterval-time.Second)) {
		t.Fatal("should not refresh latest header before idle interval")
	}
	if !shouldRefreshEvmLatestHeader(lastUpdate, false, lastUpdate.Add(evmStatsIdleInterval)) {
		t.Fatal("should refresh latest header at idle interval")
	}
	if shouldRefreshEvmLatestHeader(lastUpdate, true, lastUpdate.Add(evmStatsIdleInterval+time.Second)) {
		t.Fatal("should not refresh latest header while request is in flight")
	}
}

func TestResolveChainWsURLDisabledRow(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	node := &mdb.RpcNode{
		Network: mdb.NetworkEthereum,
		Url:     "wss://disabled.example.com",
		Type:    mdb.RpcNodeTypeWs,
		Weight:  1,
		Enabled: true,
		Status:  mdb.RpcNodeStatusOk,
	}
	if err := dao.Mdb.Create(node).Error; err != nil {
		t.Fatalf("seed rpc_node: %v", err)
	}
	if err := dao.Mdb.Model(node).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable rpc_node: %v", err)
	}

	if got, ok := resolveChainWsURL(mdb.NetworkEthereum, "[TEST]"); ok {
		t.Fatalf("resolveChainWsURL() = (%q, true), want false", got)
	}
}
