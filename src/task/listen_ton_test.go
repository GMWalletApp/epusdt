package task

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/model/service"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/jetton"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

type fakeTonShardAPI struct {
	ton.APIClientWrapped
	shorts             []ton.TransactionShortInfo
	shortsByShardSeq   map[uint32][]ton.TransactionShortInfo
	incomplete         bool
	shards             []*ton.BlockIDExt
	shardsErr          error
	lookupBlocks       map[uint32]*ton.BlockIDExt
	lookupSeqs         []uint32
	txByLT             map[uint64]*tlb.Transaction
	fetchedLTs         []uint64
	fetchedAddrs       []string
	fetchedShardSeqNos []uint32
}

type fakeTonScannerAPI struct {
	ton.APIClientWrapped
	head              *ton.BlockIDExt
	shards            []*ton.BlockIDExt
	shardsErr         error
	lookups           []uint32
	cancel            context.CancelFunc
	cancelAtLookupSeq uint32
}

func (f *fakeTonScannerAPI) CurrentMasterchainInfo(context.Context) (*ton.BlockIDExt, error) {
	return f.head, nil
}

func (f *fakeTonScannerAPI) WaitForBlock(uint32) ton.APIClientWrapped {
	return f
}

func (f *fakeTonScannerAPI) LookupBlock(_ context.Context, workchain int32, shard int64, seqno uint32) (*ton.BlockIDExt, error) {
	f.lookups = append(f.lookups, seqno)
	if f.cancel != nil && f.cancelAtLookupSeq == seqno {
		f.cancel()
	}
	return &ton.BlockIDExt{
		Workchain: workchain,
		Shard:     shard,
		SeqNo:     seqno,
		RootHash:  bytes.Repeat([]byte{byte(seqno)}, 32),
		FileHash:  bytes.Repeat([]byte{byte(seqno + 1)}, 32),
	}, nil
}

func (f *fakeTonScannerAPI) GetBlockShardsInfo(context.Context, *ton.BlockIDExt) ([]*ton.BlockIDExt, error) {
	if f.shardsErr != nil {
		return nil, f.shardsErr
	}
	return f.shards, nil
}

func (f *fakeTonShardAPI) WaitForBlock(uint32) ton.APIClientWrapped {
	return f
}

func (f *fakeTonShardAPI) GetBlockTransactionsV2(_ context.Context, block *ton.BlockIDExt, _ uint32, _ ...*ton.TransactionID3) ([]ton.TransactionShortInfo, bool, error) {
	if block != nil {
		f.fetchedShardSeqNos = append(f.fetchedShardSeqNos, block.SeqNo)
	}
	return f.blockShorts(block), f.incomplete, nil
}

func (f *fakeTonShardAPI) blockShorts(block *ton.BlockIDExt) []ton.TransactionShortInfo {
	if block != nil && f.shortsByShardSeq != nil {
		if shorts, ok := f.shortsByShardSeq[block.SeqNo]; ok {
			return shorts
		}
	}
	return f.shorts
}

func (f *fakeTonShardAPI) GetBlockShardsInfo(context.Context, *ton.BlockIDExt) ([]*ton.BlockIDExt, error) {
	if f.shardsErr != nil {
		return nil, f.shardsErr
	}
	return f.shards, nil
}

func (f *fakeTonShardAPI) LookupBlock(_ context.Context, workchain int32, shard int64, seqno uint32) (*ton.BlockIDExt, error) {
	f.lookupSeqs = append(f.lookupSeqs, seqno)
	if f.lookupBlocks != nil {
		if block, ok := f.lookupBlocks[seqno]; ok {
			return block, nil
		}
	}
	return &ton.BlockIDExt{Workchain: workchain, Shard: shard, SeqNo: seqno}, nil
}

func (f *fakeTonShardAPI) GetTransaction(_ context.Context, _ *ton.BlockIDExt, addr *address.Address, lt uint64) (*tlb.Transaction, error) {
	f.fetchedLTs = append(f.fetchedLTs, lt)
	f.fetchedAddrs = append(f.fetchedAddrs, addressutil.TonRawAddressObjectKey(addr))
	tx := f.txByLT[lt]
	if tx == nil {
		return nil, fmt.Errorf("missing tx lt=%d", lt)
	}
	return tx, nil
}

func seedTonScannerWallet(t *testing.T) {
	t.Helper()
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, "EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I"); err != nil {
		t.Fatalf("seed TON wallet: %v", err)
	}
}

func TestResolveTonLiteNodeSkipsCoolingPrimary(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	data.ResetRpcFailoverForTest()
	t.Cleanup(data.ResetRpcFailoverForTest)

	rows := []mdb.RpcNode{
		{Network: mdb.NetworkTon, Url: " https://primary-ton.example.com/global.config.json ", Type: mdb.RpcNodeTypeLite, Weight: 100, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkTon, Url: "https://backup-ton.example.com/global.config.json", Type: mdb.RpcNodeTypeLite, Weight: 1, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkTon, Url: "https://manual-ton.example.com/global.config.json", Type: mdb.RpcNodeTypeLite, Weight: 1000, Enabled: true, Purpose: mdb.RpcNodePurposeManualVerify, Status: mdb.RpcNodeStatusOk},
		{Network: mdb.NetworkTon, Url: "https://http-ton.example.com", Type: mdb.RpcNodeTypeHttp, Weight: 2000, Enabled: true, Purpose: mdb.RpcNodePurposeGeneral, Status: mdb.RpcNodeStatusOk},
	}
	if err := dao.Mdb.Create(&rows).Error; err != nil {
		t.Fatalf("seed rpc_nodes: %v", err)
	}

	primary, ok := resolveTonLiteNode()
	if !ok {
		t.Fatalf("resolveTonLiteNode() ok=false, want true")
	}
	if primary.Url != "https://primary-ton.example.com/global.config.json" {
		t.Fatalf("primary node = %#v, want trimmed primary lite node", primary)
	}
	for i := 0; i < data.RpcFailoverThreshold; i++ {
		data.RecordRpcNodeFailure(primary.ID)
	}

	got, ok := resolveTonLiteNode()
	if !ok {
		t.Fatalf("resolveTonLiteNode() after cooldown ok=false, want true")
	}
	if got.Url != "https://backup-ton.example.com/global.config.json" {
		t.Fatalf("resolveTonLiteNode() after cooldown = %#v, want backup lite node", got)
	}
}

func TestRunTonScannerInitializesCursorAtCurrentHead(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedTonScannerWallet(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &fakeTonScannerAPI{head: &ton.BlockIDExt{
		Workchain: address.MasterchainID,
		Shard:     tonMasterShard,
		SeqNo:     77,
		RootHash:  bytes.Repeat([]byte{0x77}, 32),
		FileHash:  bytes.Repeat([]byte{0x78}, 32),
	}}
	cursor := &tonRuntimeCursor{}
	if err := runTonScanner(ctx, api, newTonJettonWalletCache(), cursor); err != nil {
		t.Fatalf("runTonScanner(): %v", err)
	}
	if !cursor.initialized {
		t.Fatal("cursor not initialized")
	}
	if cursor.lastMasterSeqNo != 77 {
		t.Fatalf("cursor seqno = %d, want 77", cursor.lastMasterSeqNo)
	}
	if len(api.lookups) != 0 {
		t.Fatalf("initial cursor should not backfill, lookups=%#v", api.lookups)
	}
}

func TestRunTonScannerIdleStatesPreserveInitializedCursor(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T)
	}{
		{
			name: "chain disabled",
			setup: func(t *testing.T) {
				t.Helper()
				seedTonScannerWallet(t)
				if err := data.UpdateChainFields(mdb.NetworkTon, map[string]interface{}{"enabled": false}); err != nil {
					t.Fatalf("disable TON chain: %v", err)
				}
			},
		},
		{
			name: "no enabled wallets",
			setup: func(t *testing.T) {
				t.Helper()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := testutil.SetupTestDatabases(t)
			defer cleanup()
			tt.setup(t)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			api := &fakeTonScannerAPI{
				head: &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 11},
			}
			cursor := &tonRuntimeCursor{initialized: true, lastMasterSeqNo: 10}
			if err := runTonScanner(ctx, api, newTonJettonWalletCache(), cursor); err != nil {
				t.Fatalf("runTonScanner(): %v", err)
			}
			if !cursor.initialized || cursor.lastMasterSeqNo != 10 {
				t.Fatalf("cursor changed during idle state: initialized=%v seqno=%d, want initialized=true seqno=10", cursor.initialized, cursor.lastMasterSeqNo)
			}
		})
	}
}

func TestRunTonScannerDoesNotAdvanceCursorOnMasterProcessingFailure(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedTonScannerWallet(t)

	api := &fakeTonScannerAPI{
		head:      &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 11},
		shardsErr: errors.New("shard fetch failed"),
	}
	cursor := &tonRuntimeCursor{initialized: true, lastMasterSeqNo: 10}
	err := runTonScanner(context.Background(), api, newTonJettonWalletCache(), cursor)
	if err == nil {
		t.Fatal("expected scanner failure")
	}
	if cursor.lastMasterSeqNo != 10 {
		t.Fatalf("cursor advanced after failed block: %d, want 10", cursor.lastMasterSeqNo)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api = &fakeTonScannerAPI{
		head:   &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 11},
		shards: nil,
	}
	if err := runTonScanner(ctx, api, newTonJettonWalletCache(), cursor); err != nil {
		t.Fatalf("runTonScanner retry: %v", err)
	}
	if len(api.lookups) != 1 || api.lookups[0] != 11 {
		t.Fatalf("retry lookups = %#v, want [11]", api.lookups)
	}
	if cursor.lastMasterSeqNo != 11 {
		t.Fatalf("cursor seqno after retry = %d, want 11", cursor.lastMasterSeqNo)
	}
}

func TestRunTonScannerDoesNotMarkCursorInitializedOnInitialShardFailure(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedTonScannerWallet(t)

	api := &fakeTonScannerAPI{
		head:      &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 11},
		shardsErr: errors.New("initial shard fetch failed"),
	}
	cursor := &tonRuntimeCursor{}
	err := runTonScanner(context.Background(), api, newTonJettonWalletCache(), cursor)
	if err == nil {
		t.Fatal("expected scanner failure")
	}
	if cursor.initialized {
		t.Fatal("cursor was marked initialized after failed initial shard cursor setup")
	}
	if cursor.lastMasterSeqNo != 0 {
		t.Fatalf("cursor master seqno = %d, want 0", cursor.lastMasterSeqNo)
	}
}

func TestRunTonScannerRespectsMinConfirmations(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedTonScannerWallet(t)
	if err := data.UpdateChainFields(mdb.NetworkTon, map[string]interface{}{"min_confirmations": 3}); err != nil {
		t.Fatalf("set TON min confirmations: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &fakeTonScannerAPI{
		head: &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 12},
	}
	cursor := &tonRuntimeCursor{initialized: true, lastMasterSeqNo: 10}
	if err := runTonScanner(ctx, api, newTonJettonWalletCache(), cursor); err != nil {
		t.Fatalf("runTonScanner(): %v", err)
	}
	if len(api.lookups) != 0 {
		t.Fatalf("scanner processed unconfirmed blocks, lookups=%#v", api.lookups)
	}
	if cursor.lastMasterSeqNo != 10 {
		t.Fatalf("cursor seqno = %d, want 10", cursor.lastMasterSeqNo)
	}
}

func TestRunTonScannerCatchupProcessesAtMostFiftyMasterBlocksPerTick(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedTonScannerWallet(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &fakeTonScannerAPI{
		head:   &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 100},
		shards: nil,
	}
	cursor := &tonRuntimeCursor{initialized: true, lastMasterSeqNo: 1}
	if err := runTonScanner(ctx, api, newTonJettonWalletCache(), cursor); err != nil {
		t.Fatalf("runTonScanner(): %v", err)
	}
	if len(api.lookups) != int(tonMaxMasterBlocksTick) {
		t.Fatalf("lookup count = %d, want %d", len(api.lookups), tonMaxMasterBlocksTick)
	}
	if cursor.lastMasterSeqNo != 51 {
		t.Fatalf("cursor seqno = %d, want 51", cursor.lastMasterSeqNo)
	}
}

func TestRunTonScannerCatchupContinuesUntilConfirmedHead(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedTonScannerWallet(t)

	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeTonScannerAPI{
		head:              &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 60},
		shards:            nil,
		cancel:            cancel,
		cancelAtLookupSeq: 60,
	}
	cursor := &tonRuntimeCursor{initialized: true, lastMasterSeqNo: 1}
	if err := runTonScanner(ctx, api, newTonJettonWalletCache(), cursor); err != nil {
		t.Fatalf("runTonScanner(): %v", err)
	}
	if len(api.lookups) != 59 {
		t.Fatalf("lookup count = %d, want 59", len(api.lookups))
	}
	if api.lookups[0] != 2 || api.lookups[len(api.lookups)-1] != 60 {
		t.Fatalf("lookups = %#v, want continuous 2..60", api.lookups)
	}
	for i, seq := range api.lookups {
		want := uint32(i + 2)
		if seq != want {
			t.Fatalf("lookup[%d] = %d, want %d", i, seq, want)
		}
	}
	if cursor.lastMasterSeqNo != 60 {
		t.Fatalf("cursor seqno = %d, want 60", cursor.lastMasterSeqNo)
	}
}

func TestProcessTonShardBlockFetchesOnlyWatchedWalletTransactions(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	receive := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	unwatched := address.NewAddress(0, byte(receive.Workchain()), bytes.Repeat([]byte{0x77}, 32)).Bounce(false).Testnet(false)
	sender := address.NewAddress(0, byte(receive.Workchain()), bytes.Repeat([]byte{0x88}, 32)).Bounce(false).Testnet(false)

	tx := &tlb.Transaction{
		LT:   2,
		Now:  uint32(time.Now().Unix()),
		Hash: bytes.Repeat([]byte{0x99}, 32),
		IO: struct {
			In  *tlb.Message      `tlb:"maybe ^"`
			Out *tlb.MessagesList `tlb:"maybe ^"`
		}{
			In: &tlb.Message{
				MsgType: tlb.MsgTypeInternal,
				Msg: &tlb.InternalMessage{
					SrcAddr: sender,
					DstAddr: receive,
					Amount:  tlb.MustFromTON("1"),
					Body:    cell.BeginCell().EndCell(),
				},
			},
		},
	}

	api := &fakeTonShardAPI{
		shorts: []ton.TransactionShortInfo{
			{Account: unwatched.Data(), LT: 1, Hash: bytes.Repeat([]byte{0x01}, 32)},
			{Account: receive.Data(), LT: 2, Hash: bytes.Repeat([]byte{0x02}, 32)},
		},
		txByLT: map[uint64]*tlb.Transaction{2: tx},
	}
	state := &tonWatchState{
		wallets: map[string]*address.Address{
			addressutil.TonRawAddressObjectKey(receive): receive,
		},
		nativeToken: &mdb.ChainToken{BaseModel: mdb.BaseModel{ID: 1}, Network: mdb.NetworkTon, Symbol: service.TonNativeSymbol, Decimals: 9, Enabled: true},
	}

	master := &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 10}
	shard := &ton.BlockIDExt{Workchain: receive.Workchain(), Shard: 0, SeqNo: 9}
	err := processTonShardBlock(context.Background(), api, master, shard, state, newTonJettonWalletCache())
	if err != nil {
		t.Fatalf("processTonShardBlock(): %v", err)
	}
	if len(api.fetchedLTs) != 1 || api.fetchedLTs[0] != 2 {
		t.Fatalf("fetched LTs = %#v, want [2]", api.fetchedLTs)
	}
	if len(api.fetchedAddrs) != 1 || api.fetchedAddrs[0] != addressutil.TonRawAddressObjectKey(receive) {
		t.Fatalf("fetched addresses = %#v, want watched receive address", api.fetchedAddrs)
	}
}

func TestProcessTonMasterBlockWithCursorScansSkippedShardBlocks(t *testing.T) {
	receive := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	sender := address.NewAddress(0, byte(receive.Workchain()), bytes.Repeat([]byte{0x88}, 32)).Bounce(false).Testnet(false)
	txHash := bytes.Repeat([]byte{0x42}, 32)
	tx := &tlb.Transaction{
		LT:   88,
		Now:  uint32(time.Now().Unix()),
		Hash: txHash,
		IO: struct {
			In  *tlb.Message      `tlb:"maybe ^"`
			Out *tlb.MessagesList `tlb:"maybe ^"`
		}{
			In: &tlb.Message{
				MsgType: tlb.MsgTypeInternal,
				Msg: &tlb.InternalMessage{
					SrcAddr: sender,
					DstAddr: receive,
					Amount:  tlb.MustFromTON("1"),
					Body:    cell.BeginCell().EndCell(),
				},
			},
		},
	}

	currentShard := &ton.BlockIDExt{Workchain: receive.Workchain(), Shard: tonMasterShard, SeqNo: 89}
	skippedShard := &ton.BlockIDExt{Workchain: receive.Workchain(), Shard: tonMasterShard, SeqNo: 88}
	api := &fakeTonShardAPI{
		shards:       []*ton.BlockIDExt{currentShard},
		lookupBlocks: map[uint32]*ton.BlockIDExt{88: skippedShard},
		shortsByShardSeq: map[uint32][]ton.TransactionShortInfo{
			88: {{Account: receive.Data(), LT: tx.LT, Hash: txHash}},
			89: nil,
		},
		txByLT: map[uint64]*tlb.Transaction{tx.LT: tx},
	}
	state := &tonWatchState{
		wallets: map[string]*address.Address{
			addressutil.TonRawAddressObjectKey(receive): receive,
		},
	}
	cursorKey := tonShardCursorKey(currentShard)
	cursor := &tonRuntimeCursor{
		initialized:     true,
		lastMasterSeqNo: 10,
		shardSeqNo:      map[string]uint32{cursorKey: 87},
	}

	master := &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 10}
	err := processTonMasterBlockWithCursor(context.Background(), api, master, state, newTonJettonWalletCache(), cursor)
	if err != nil {
		t.Fatalf("processTonMasterBlockWithCursor(): %v", err)
	}
	if len(api.lookupSeqs) != 1 || api.lookupSeqs[0] != 88 {
		t.Fatalf("lookup shard seqs = %#v, want [88]", api.lookupSeqs)
	}
	if len(api.fetchedShardSeqNos) != 2 || api.fetchedShardSeqNos[0] != 88 || api.fetchedShardSeqNos[1] != 89 {
		t.Fatalf("fetched shard seqs = %#v, want [88 89]", api.fetchedShardSeqNos)
	}
	if len(api.fetchedLTs) != 1 || api.fetchedLTs[0] != tx.LT {
		t.Fatalf("fetched LTs = %#v, want [%d]", api.fetchedLTs, tx.LT)
	}
	if cursor.shardSeqNo[cursorKey] != 89 {
		t.Fatalf("shard cursor = %d, want 89", cursor.shardSeqNo[cursorKey])
	}
}

func TestProcessTonShardBlockMatchesJettonUSDTOrder(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting("rate", "rate.forced_rate_list", `{"cny":{"usdt":0.1}}`, "json"); err != nil {
		t.Fatalf("set forced rate: %v", err)
	}
	receive := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	if _, err := data.AddWalletAddressWithNetwork(mdb.NetworkTon, receive.StringRaw()); err != nil {
		t.Fatalf("add ton wallet: %v", err)
	}
	resp, err := service.CreateTransaction(&request.CreateTransactionRequest{
		OrderId:   "order_ton_usdt_shard_1",
		Currency:  "CNY",
		Token:     "USDT",
		Network:   mdb.NetworkTon,
		Amount:    10,
		NotifyUrl: "https://93.184.216.34/callback",
	}, nil)
	if err != nil {
		t.Fatalf("create TON USDT order: %v", err)
	}

	sender := address.NewAddress(0, 0, bytes.Repeat([]byte{0x31}, 32)).Bounce(false).Testnet(false)
	jettonWallet := address.NewAddress(0, byte(receive.Workchain()), bytes.Repeat([]byte{0x44}, 32)).Bounce(false).Testnet(false)
	body, err := tlb.ToCell(jetton.TransferNotification{
		QueryID:        7,
		Amount:         tlb.MustFromNano(big.NewInt(1_000_000), 6),
		Sender:         sender,
		ForwardPayload: cell.BeginCell().EndCell(),
	})
	if err != nil {
		t.Fatalf("build jetton notification body: %v", err)
	}
	tx := &tlb.Transaction{
		LT:   22,
		Now:  uint32(time.Now().Add(time.Second).Unix()),
		Hash: bytes.Repeat([]byte{0x42}, 32),
		IO: struct {
			In  *tlb.Message      `tlb:"maybe ^"`
			Out *tlb.MessagesList `tlb:"maybe ^"`
		}{
			In: &tlb.Message{
				MsgType: tlb.MsgTypeInternal,
				Msg: &tlb.InternalMessage{
					SrcAddr: jettonWallet,
					DstAddr: receive,
					Amount:  tlb.FromNanoTONU(1),
					Body:    body,
				},
			},
		},
	}

	usdtMaster, err := addressutil.ParseTonMainnetAddress("0:b113a994b5024a16719f69139328eb759596c38a25f59028b146fecdc3621dfe")
	if err != nil {
		t.Fatalf("parse USDT jetton master: %v", err)
	}
	cache := newTonJettonWalletCache()
	ownerRaw := addressutil.TonRawAddressObjectKey(receive)
	cache.walletByOwnerToken[usdtMaster.Raw+"|"+ownerRaw] = addressutil.TonRawAddressObjectKey(jettonWallet)

	api := &fakeTonShardAPI{
		shorts: []ton.TransactionShortInfo{
			{Account: receive.Data(), LT: tx.LT, Hash: tx.Hash},
		},
		txByLT: map[uint64]*tlb.Transaction{tx.LT: tx},
	}
	state := &tonWatchState{
		wallets: map[string]*address.Address{
			ownerRaw: receive,
		},
		jettonTokens: []mdb.ChainToken{
			{BaseModel: mdb.BaseModel{ID: 2}, Network: mdb.NetworkTon, Symbol: "USDT", ContractAddress: usdtMaster.Raw, Decimals: 6, Enabled: true},
		},
	}
	master := &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 10}
	shard := &ton.BlockIDExt{Workchain: receive.Workchain(), Shard: 0, SeqNo: 9}
	if err := processTonShardBlock(context.Background(), api, master, shard, state, cache); err != nil {
		t.Fatalf("processTonShardBlock(): %v", err)
	}

	order, err := data.GetOrderInfoByTradeId(resp.TradeId)
	if err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if order.Status != mdb.StatusPaySuccess {
		t.Fatalf("order status = %d, want %d", order.Status, mdb.StatusPaySuccess)
	}
	wantBlockID := service.TonCanonicalBlockTransactionID(receive.StringRaw(), tx.LT, hex.EncodeToString(tx.Hash))
	if order.BlockTransactionId != wantBlockID {
		t.Fatalf("block transaction id = %q, want %q", order.BlockTransactionId, wantBlockID)
	}
	lock, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, receive.StringRaw(), "USDT", resp.ActualAmount)
	if err != nil {
		t.Fatalf("lookup TON USDT lock: %v", err)
	}
	if lock != "" {
		t.Fatalf("TON USDT lock still exists after shard processing: %s", lock)
	}
}

func TestProcessTonMasterBlockScansMasterchainWalletTransactions(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	masterchainID := address.MasterchainID
	masterchainByte := byte(int8(masterchainID))
	receive := address.NewAddress(0, masterchainByte, bytes.Repeat([]byte{0x66}, 32)).Bounce(false).Testnet(false)
	sender := address.NewAddress(0, masterchainByte, bytes.Repeat([]byte{0x88}, 32)).Bounce(false).Testnet(false)
	tx := &tlb.Transaction{
		LT:   2,
		Now:  uint32(time.Now().Unix()),
		Hash: bytes.Repeat([]byte{0x99}, 32),
		IO: struct {
			In  *tlb.Message      `tlb:"maybe ^"`
			Out *tlb.MessagesList `tlb:"maybe ^"`
		}{
			In: &tlb.Message{
				MsgType: tlb.MsgTypeInternal,
				Msg: &tlb.InternalMessage{
					SrcAddr: sender,
					DstAddr: receive,
					Amount:  tlb.MustFromTON("1"),
					Body:    cell.BeginCell().EndCell(),
				},
			},
		},
	}
	api := &fakeTonShardAPI{
		shorts: []ton.TransactionShortInfo{
			{Account: receive.Data(), LT: 2, Hash: bytes.Repeat([]byte{0x02}, 32)},
		},
		txByLT: map[uint64]*tlb.Transaction{2: tx},
	}
	state := &tonWatchState{
		wallets: map[string]*address.Address{
			addressutil.TonRawAddressObjectKey(receive): receive,
		},
		nativeToken: &mdb.ChainToken{BaseModel: mdb.BaseModel{ID: 1}, Network: mdb.NetworkTon, Symbol: service.TonNativeSymbol, Decimals: 9, Enabled: true},
	}

	master := &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 10}
	if err := processTonMasterBlock(context.Background(), api, master, state, newTonJettonWalletCache()); err != nil {
		t.Fatalf("processTonMasterBlock(): %v", err)
	}
	if len(api.fetchedLTs) != 1 || api.fetchedLTs[0] != 2 {
		t.Fatalf("fetched LTs = %#v, want [2]", api.fetchedLTs)
	}
	if len(api.fetchedAddrs) != 1 || api.fetchedAddrs[0] != addressutil.TonRawAddressObjectKey(receive) {
		t.Fatalf("fetched addresses = %#v, want masterchain receive address", api.fetchedAddrs)
	}
}

func TestProcessTonShardBlockFailsOnIncompleteEmptyPage(t *testing.T) {
	api := &fakeTonShardAPI{incomplete: true}
	state := &tonWatchState{wallets: map[string]*address.Address{}}
	master := &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 10}
	shard := &ton.BlockIDExt{Workchain: 0, Shard: 0, SeqNo: 9}

	err := processTonShardBlock(context.Background(), api, master, shard, state, newTonJettonWalletCache())
	if err == nil {
		t.Fatal("expected incomplete empty page error")
	}
}

func TestProcessTonShardBlockFailsWhenJettonWalletsCannotBeResolved(t *testing.T) {
	receive := address.MustParseAddr("EQC6KV4zs8TJtSZapOrRFmqSkxzpq-oSCoxekQRKElf4nC1I")
	sender := address.NewAddress(0, byte(receive.Workchain()), bytes.Repeat([]byte{0x88}, 32)).Bounce(false).Testnet(false)
	tx := &tlb.Transaction{
		LT:   2,
		Now:  uint32(time.Now().Unix()),
		Hash: bytes.Repeat([]byte{0x99}, 32),
		IO: struct {
			In  *tlb.Message      `tlb:"maybe ^"`
			Out *tlb.MessagesList `tlb:"maybe ^"`
		}{
			In: &tlb.Message{
				MsgType: tlb.MsgTypeInternal,
				Msg: &tlb.InternalMessage{
					SrcAddr: sender,
					DstAddr: receive,
					Amount:  tlb.MustFromTON("1"),
					Body:    cell.BeginCell().EndCell(),
				},
			},
		},
	}
	api := &fakeTonShardAPI{
		shorts: []ton.TransactionShortInfo{
			{Account: receive.Data(), LT: 2, Hash: bytes.Repeat([]byte{0x02}, 32)},
		},
		txByLT: map[uint64]*tlb.Transaction{2: tx},
	}
	state := &tonWatchState{
		wallets: map[string]*address.Address{
			addressutil.TonRawAddressObjectKey(receive): receive,
		},
		jettonTokens: []mdb.ChainToken{
			{BaseModel: mdb.BaseModel{ID: 1}, Network: mdb.NetworkTon, Symbol: "USDT", ContractAddress: "not-a-ton-address", Decimals: 6, Enabled: true},
		},
	}

	master := &ton.BlockIDExt{Workchain: address.MasterchainID, Shard: tonMasterShard, SeqNo: 10}
	shard := &ton.BlockIDExt{Workchain: receive.Workchain(), Shard: 0, SeqNo: 9}
	err := processTonShardBlock(context.Background(), api, master, shard, state, newTonJettonWalletCache())
	if err == nil {
		t.Fatal("expected jetton wallet resolution failure")
	}
	if len(api.fetchedLTs) != 1 || api.fetchedLTs[0] != 2 {
		t.Fatalf("fetched LTs = %#v, want [2]", api.fetchedLTs)
	}
}
