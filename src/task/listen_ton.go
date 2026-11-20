package task

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	addressutil "github.com/GMWalletApp/epusdt/util/address"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/jetton"
)

const (
	tonMasterShard           = int64(-0x8000000000000000)
	tonScannerConnectTimeout = 20 * time.Second
	tonScannerRequestTimeout = 15 * time.Second
	tonScannerRetryDelay     = 3 * time.Second
	tonCatchupYield          = 250 * time.Millisecond
	tonMaxMasterBlocksTick   = uint32(50)
	tonBlockTxPageSize       = uint32(256)
)

type tonWatchState struct {
	wallets      map[string]*address.Address
	nativeToken  *mdb.ChainToken
	jettonTokens []mdb.ChainToken
}

func (s *tonWatchState) hasWalletInWorkchain(workchain int32) bool {
	if s == nil {
		return false
	}
	for _, wallet := range s.wallets {
		if wallet != nil && wallet.Workchain() == workchain {
			return true
		}
	}
	return false
}

type tonRuntimeCursor struct {
	initialized     bool
	lastMasterSeqNo uint32
	shardSeqNo      map[string]uint32
}

func (c *tonRuntimeCursor) ensureShardSeqNo() {
	if c.shardSeqNo == nil {
		c.shardSeqNo = make(map[string]uint32)
	}
}

type tonJettonWalletCache struct {
	sync.Mutex
	walletByOwnerToken map[string]string
}

func newTonJettonWalletCache() *tonJettonWalletCache {
	return &tonJettonWalletCache{walletByOwnerToken: make(map[string]string)}
}

// StartTonBlockScannerListener runs a forward-only TON masterchain scanner.
func StartTonBlockScannerListener() {
	cache := newTonJettonWalletCache()
	cursor := &tonRuntimeCursor{}
	for {
		chain, interval := tonChainConfig()
		if chain == nil || !chain.Enabled {
			sleepOrDone(context.Background(), interval)
			continue
		}
		node, ok := resolveTonLiteNode()
		if !ok {
			sleepOrDone(context.Background(), interval)
			continue
		}
		log.Sugar.Infof("[TON] connecting using lite node %s", data.RpcNodeLogLabel(node))
		api, closeFn, err := connectTonLiteAPI(node)
		if err != nil {
			log.Sugar.Warnf("[TON] connect lite node %s failed: %v", data.RpcNodeLogLabel(node), err)
			recordTonLiteNodeFailure(node, "connect")
			sleepOrDone(context.Background(), tonScannerRetryDelay)
			continue
		}
		log.Sugar.Infof("[TON] connected using lite node %s", data.RpcNodeLogLabel(node))
		err = runTonScanner(context.Background(), api, cache, cursor)
		closeFn()
		if err != nil {
			log.Sugar.Warnf("[TON] scanner stopped: %v", err)
			recordTonLiteNodeFailure(node, err.Error())
			sleepOrDone(context.Background(), tonScannerRetryDelay)
			continue
		}
	}
}

func runTonScanner(ctx context.Context, api ton.APIClientWrapped, cache *tonJettonWalletCache, cursor *tonRuntimeCursor) error {
	if cursor == nil {
		cursor = &tonRuntimeCursor{}
	}
	watchSignature := ""
	for {
		chain, interval := tonChainConfig()
		if chain == nil || !chain.Enabled {
			return nil
		}
		state, err := loadTonWatchState()
		if err != nil {
			return err
		}
		if len(state.wallets) == 0 || (state.nativeToken == nil && len(state.jettonTokens) == 0) {
			log.Sugar.Debug("[TON] no enabled wallets or tokens, idling")
			if !sleepOrDone(ctx, interval) {
				return nil
			}
			continue
		}
		if sig := tonWatchStateSignature(state); sig != watchSignature {
			watchSignature = sig
			log.Sugar.Infof(
				"[TON] watching wallets=%d native_ton=%t jetton_tokens=%d wallet_workchains=%s",
				len(state.wallets),
				state.nativeToken != nil,
				len(state.jettonTokens),
				tonWalletWorkchainSummary(state),
			)
		}

		head, err := api.CurrentMasterchainInfo(ctx)
		if err != nil {
			data.RecordRpcFailure(mdb.NetworkTon)
			return fmt.Errorf("current masterchain info: %w", err)
		}
		confirmedHeadSeqNo := tonConfirmedMasterSeqNo(head.SeqNo, chain.MinConfirmations)
		data.RecordRpcSuccess(mdb.NetworkTon)
		data.RecordRpcBlockHeight(mdb.NetworkTon, int64(head.SeqNo))

		if !cursor.initialized {
			if err = initializeTonRuntimeCursor(ctx, api, head, confirmedHeadSeqNo, cursor); err != nil {
				data.RecordRpcFailure(mdb.NetworkTon)
				return err
			}
			log.Sugar.Infof(
				"[TON] current masterchain head=%d confirmed=%d root=%s file=%s",
				head.SeqNo,
				confirmedHeadSeqNo,
				shortTonHash(head.RootHash),
				shortTonHash(head.FileHash),
			)
			log.Sugar.Infof("[TON] initialized runtime cursor at masterchain seqno=%d head=%d", cursor.lastMasterSeqNo, head.SeqNo)
			if !sleepOrDone(ctx, interval) {
				return nil
			}
			continue
		}
		if cursor.lastMasterSeqNo >= confirmedHeadSeqNo {
			if !sleepOrDone(ctx, interval) {
				return nil
			}
			continue
		}

		target := cursor.lastMasterSeqNo + tonMaxMasterBlocksTick
		if target > confirmedHeadSeqNo {
			target = confirmedHeadSeqNo
		}
		log.Sugar.Infof("[TON] processing masterchain blocks from=%d to=%d head=%d confirmed=%d", cursor.lastMasterSeqNo+1, target, head.SeqNo, confirmedHeadSeqNo)
		for seq := cursor.lastMasterSeqNo + 1; seq <= target; seq++ {
			master, err := api.WaitForBlock(seq).LookupBlock(ctx, address.MasterchainID, tonMasterShard, seq)
			if err != nil {
				data.RecordRpcFailure(mdb.NetworkTon)
				return fmt.Errorf("lookup masterchain seqno=%d: %w", seq, err)
			}
			if err = processTonMasterBlockWithCursor(ctx, api, master, state, cache, cursor); err != nil {
				data.RecordRpcFailure(mdb.NetworkTon)
				return fmt.Errorf("process masterchain seqno=%d: %w", seq, err)
			}
			cursor.lastMasterSeqNo = master.SeqNo
			data.RecordRpcSuccess(mdb.NetworkTon)
			data.RecordRpcBlockHeight(mdb.NetworkTon, int64(seq))
		}
		if target < confirmedHeadSeqNo {
			if !sleepOrDone(ctx, tonCatchupYield) {
				return nil
			}
		}
	}
}

func tonConfirmedMasterSeqNo(headSeqNo uint32, minConfirmations int) uint32 {
	if minConfirmations <= 1 {
		return headSeqNo
	}
	conf := uint32(minConfirmations)
	if headSeqNo < conf-1 {
		return 0
	}
	return headSeqNo - conf + 1
}

func initializeTonRuntimeCursor(ctx context.Context, api ton.APIClientWrapped, head *ton.BlockIDExt, confirmedHeadSeqNo uint32, cursor *tonRuntimeCursor) error {
	if cursor == nil {
		return nil
	}
	if head == nil {
		return fmt.Errorf("current masterchain head is nil")
	}
	initMaster := head
	if confirmedHeadSeqNo > 0 && confirmedHeadSeqNo != head.SeqNo {
		var err error
		initMaster, err = api.WaitForBlock(confirmedHeadSeqNo).LookupBlock(ctx, address.MasterchainID, tonMasterShard, confirmedHeadSeqNo)
		if err != nil {
			return fmt.Errorf("lookup initial confirmed masterchain seqno=%d: %w", confirmedHeadSeqNo, err)
		}
	}
	if confirmedHeadSeqNo > 0 {
		if err := initializeTonShardCursors(ctx, api, initMaster, cursor); err != nil {
			return fmt.Errorf("initialize shard cursors masterchain seqno=%d: %w", initMaster.SeqNo, err)
		}
	}
	cursor.initialized = true
	cursor.lastMasterSeqNo = confirmedHeadSeqNo
	return nil
}

func processTonMasterBlock(ctx context.Context, api ton.APIClientWrapped, master *ton.BlockIDExt, state *tonWatchState, cache *tonJettonWalletCache) error {
	return processTonMasterBlockWithCursor(ctx, api, master, state, cache, nil)
}

func processTonMasterBlockWithCursor(ctx context.Context, api ton.APIClientWrapped, master *ton.BlockIDExt, state *tonWatchState, cache *tonJettonWalletCache, cursor *tonRuntimeCursor) error {
	if state.hasWalletInWorkchain(address.MasterchainID) {
		if err := processTonShardBlock(ctx, api, master, master, state, cache); err != nil {
			return fmt.Errorf("process masterchain transactions: %w", err)
		}
	}
	shards, err := api.WaitForBlock(master.SeqNo).GetBlockShardsInfo(ctx, master)
	if err != nil {
		return err
	}
	log.Sugar.Debugf(
		"[TON] masterchain seqno=%d root=%s file=%s shard_blocks=%d workchains=%s",
		master.SeqNo,
		shortTonHash(master.RootHash),
		shortTonHash(master.FileHash),
		len(shards),
		tonShardWorkchainSummary(shards),
	)
	if cursor != nil {
		return processTonShardBlocksWithCursor(ctx, api, master, shards, state, cache, cursor)
	}
	for _, shard := range shards {
		if err = processTonShardBlock(ctx, api, master, shard, state, cache); err != nil {
			return err
		}
	}
	return nil
}

func initializeTonShardCursors(ctx context.Context, api ton.APIClientWrapped, master *ton.BlockIDExt, cursor *tonRuntimeCursor) error {
	if cursor == nil || master == nil {
		return nil
	}
	shards, err := api.WaitForBlock(master.SeqNo).GetBlockShardsInfo(ctx, master)
	if err != nil {
		return err
	}
	cursor.ensureShardSeqNo()
	for _, shard := range shards {
		if shard == nil {
			continue
		}
		cursor.shardSeqNo[tonShardCursorKey(shard)] = shard.SeqNo
	}
	log.Sugar.Infof(
		"[TON] initialized shard cursors masterchain seqno=%d shard_blocks=%d workchains=%s",
		master.SeqNo,
		len(shards),
		tonShardWorkchainSummary(shards),
	)
	return nil
}

func processTonShardBlocksWithCursor(ctx context.Context, api ton.APIClientWrapped, master *ton.BlockIDExt, shards []*ton.BlockIDExt, state *tonWatchState, cache *tonJettonWalletCache, cursor *tonRuntimeCursor) error {
	if cursor == nil {
		return nil
	}
	cursor.ensureShardSeqNo()
	for _, shard := range shards {
		if shard == nil {
			continue
		}
		key := tonShardCursorKey(shard)
		lastSeqNo, ok := cursor.shardSeqNo[key]
		if !state.hasWalletInWorkchain(shard.Workchain) {
			cursor.shardSeqNo[key] = shard.SeqNo
			continue
		}

		start := shard.SeqNo
		if ok {
			if shard.SeqNo <= lastSeqNo {
				continue
			}
			start = lastSeqNo + 1
		}
		if shard.SeqNo > start {
			log.Sugar.Infof(
				"[TON] processing shard blocks master_seqno=%d workchain=%d shard=%x from=%d to=%d",
				master.SeqNo,
				shard.Workchain,
				uint64(shard.Shard),
				start,
				shard.SeqNo,
			)
		}
		for seq := start; seq <= shard.SeqNo; seq++ {
			shardBlock := shard
			if seq != shard.SeqNo {
				var err error
				shardBlock, err = api.WaitForBlock(master.SeqNo).LookupBlock(ctx, shard.Workchain, shard.Shard, seq)
				if err != nil {
					return fmt.Errorf("lookup shard block workchain=%d shard=%x seqno=%d: %w", shard.Workchain, uint64(shard.Shard), seq, err)
				}
			}
			if err := processTonShardBlock(ctx, api, master, shardBlock, state, cache); err != nil {
				return err
			}
			cursor.shardSeqNo[key] = seq
		}
	}
	return nil
}

func processTonShardBlock(ctx context.Context, api ton.APIClientWrapped, master, shard *ton.BlockIDExt, state *tonWatchState, cache *tonJettonWalletCache) error {
	log.Sugar.Debugf("[TON] scanning shard master_seqno=%d workchain=%d shard=%d shard_seqno=%d", master.SeqNo, shard.Workchain, shard.Shard, shard.SeqNo)
	var after *ton.TransactionID3
	for {
		shorts, incomplete, err := api.WaitForBlock(master.SeqNo).GetBlockTransactionsV2(ctx, shard, tonBlockTxPageSize, after)
		if err != nil {
			return err
		}
		for i := range shorts {
			account := addressutil.TonAddressFromShortAccount(shard.Workchain, shorts[i].Account)
			if account == nil {
				continue
			}
			receive, ok := state.wallets[addressutil.TonRawAddressObjectKey(account)]
			if !ok {
				continue
			}
			log.Sugar.Debugf("[TON] watched account tx candidate master_seqno=%d workchain=%d shard=%d lt=%d account=%s", master.SeqNo, shard.Workchain, shard.Shard, shorts[i].LT, addressutil.TonRawAddressObjectKey(account))
			tx, err := api.WaitForBlock(master.SeqNo).GetTransaction(ctx, shard, account, shorts[i].LT)
			if err != nil {
				return err
			}
			if tx == nil {
				return fmt.Errorf("missing full transaction workchain=%d shard=%d seqno=%d lt=%d", shard.Workchain, shard.Shard, shard.SeqNo, shorts[i].LT)
			}
			jettonWallets, err := cache.jettonWalletTokens(ctx, api, master, receive, state.jettonTokens)
			if err != nil {
				return err
			}
			transfer, err := service.ParseTonInboundTransfer(tx, receive, state.nativeToken, jettonWallets)
			if err != nil {
				log.Sugar.Warnf("[TON] parse inbound tx lt=%d hash=%s: %v", tx.LT, hex.EncodeToString(tx.Hash), err)
				continue
			}
			if transfer == nil {
				log.Sugar.Debugf(
					"[TON] unsupported watched tx master_seqno=%d workchain=%d shard=%d lt=%d hash=%s account=%s",
					master.SeqNo,
					shard.Workchain,
					shard.Shard,
					tx.LT,
					hex.EncodeToString(tx.Hash),
					addressutil.TonRawAddressObjectKey(account),
				)
				continue
			}
			service.TryProcessTonTransfer(transfer)
		}
		if !incomplete {
			return nil
		}
		if len(shorts) == 0 {
			return fmt.Errorf("incomplete transaction page without cursor progress workchain=%d shard=%d seqno=%d", shard.Workchain, shard.Shard, shard.SeqNo)
		}
		after = shorts[len(shorts)-1].ID3()
	}
}

func (c *tonJettonWalletCache) jettonWalletTokens(ctx context.Context, api ton.APIClientWrapped, master *ton.BlockIDExt, owner *address.Address, tokens []mdb.ChainToken) (map[string]mdb.ChainToken, error) {
	out := make(map[string]mdb.ChainToken, len(tokens))
	ownerRaw := addressutil.TonRawAddressObjectKey(owner)
	for _, token := range tokens {
		masterParsed, err := addressutil.ParseTonMainnetAddress(token.ContractAddress)
		if err != nil {
			return nil, fmt.Errorf("invalid jetton master symbol=%s address=%s: %w", token.Symbol, token.ContractAddress, err)
		}
		cacheKey := masterParsed.Raw + "|" + ownerRaw
		c.Lock()
		walletRaw, ok := c.walletByOwnerToken[cacheKey]
		c.Unlock()
		if !ok {
			wallet, err := jetton.NewJettonMasterClient(api, masterParsed.Address).GetJettonWalletAtBlock(ctx, owner, master)
			if err != nil {
				return nil, fmt.Errorf("derive jetton wallet symbol=%s owner=%s: %w", token.Symbol, ownerRaw, err)
			}
			walletRaw = addressutil.TonRawAddressObjectKey(wallet.Address())
			c.Lock()
			c.walletByOwnerToken[cacheKey] = walletRaw
			c.Unlock()
		}
		out[walletRaw] = token
	}
	return out, nil
}

func loadTonWatchState() (*tonWatchState, error) {
	walletRows, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkTon)
	if err != nil {
		return nil, err
	}
	tokens, err := data.ListEnabledChainTokensByNetwork(mdb.NetworkTon)
	if err != nil {
		return nil, err
	}
	state := &tonWatchState{wallets: make(map[string]*address.Address)}
	for _, row := range walletRows {
		parsed, err := addressutil.ParseTonMainnetAddress(row.Address)
		if err != nil {
			log.Sugar.Warnf("[TON] skip invalid wallet address=%s err=%v", row.Address, err)
			continue
		}
		state.wallets[parsed.Raw] = parsed.Address
	}
	for i := range tokens {
		sym := strings.ToUpper(strings.TrimSpace(tokens[i].Symbol))
		contract := strings.TrimSpace(tokens[i].ContractAddress)
		switch {
		case sym == service.TonNativeSymbol && contract == "":
			token := tokens[i]
			state.nativeToken = &token
		case contract != "":
			state.jettonTokens = append(state.jettonTokens, tokens[i])
		}
	}
	return state, nil
}

func tonChainConfig() (*mdb.Chain, time.Duration) {
	row, err := data.GetChainByNetwork(mdb.NetworkTon)
	if err != nil || row.ID == 0 {
		return nil, 5 * time.Second
	}
	interval := time.Duration(row.ScanIntervalSec) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return row, interval
}

func tonWatchStateSignature(state *tonWatchState) string {
	if state == nil {
		return ""
	}
	return fmt.Sprintf("%d|%t|%d|%s", len(state.wallets), state.nativeToken != nil, len(state.jettonTokens), tonWalletWorkchainSummary(state))
}

func tonWalletWorkchainSummary(state *tonWatchState) string {
	if state == nil || len(state.wallets) == 0 {
		return "none"
	}
	counts := make(map[int32]int)
	for _, wallet := range state.wallets {
		if wallet == nil {
			continue
		}
		counts[wallet.Workchain()]++
	}
	return formatTonWorkchainCounts(counts)
}

func tonShardWorkchainSummary(shards []*ton.BlockIDExt) string {
	if len(shards) == 0 {
		return "none"
	}
	counts := make(map[int32]int)
	for _, shard := range shards {
		if shard == nil {
			continue
		}
		counts[shard.Workchain]++
	}
	return formatTonWorkchainCounts(counts)
}

func tonShardCursorKey(shard *ton.BlockIDExt) string {
	if shard == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", shard.Workchain, shard.Shard)
}

func formatTonWorkchainCounts(counts map[int32]int) string {
	if len(counts) == 0 {
		return "none"
	}
	workchains := make([]int, 0, len(counts))
	for workchain := range counts {
		workchains = append(workchains, int(workchain))
	}
	sort.Ints(workchains)
	parts := make([]string, 0, len(workchains))
	for _, workchain := range workchains {
		parts = append(parts, fmt.Sprintf("%d:%d", workchain, counts[int32(workchain)]))
	}
	return strings.Join(parts, ",")
}

func shortTonHash(raw []byte) string {
	hash := hex.EncodeToString(raw)
	if len(hash) > 16 {
		return hash[:16]
	}
	if hash == "" {
		return "-"
	}
	return hash
}

func resolveTonLiteNode(excludeIDs ...uint64) (mdb.RpcNode, bool) {
	node, err := data.SelectGeneralRpcNode(mdb.NetworkTon, mdb.RpcNodeTypeLite, excludeIDs...)
	if err == nil && node != nil && node.ID > 0 {
		node.Url = strings.TrimSpace(node.Url)
		if node.Url != "" {
			return *node, true
		}
		log.Sugar.Errorf("[TON] rpc_nodes id=%d has empty url", node.ID)
		return mdb.RpcNode{}, false
	}
	if err != nil {
		log.Sugar.Errorf("[TON] resolve lite rpc_nodes err=%v", err)
	} else {
		log.Sugar.Warn("[TON] no enabled lite RPC node configured in rpc_nodes")
	}
	return mdb.RpcNode{}, false
}

func connectTonLiteAPI(node mdb.RpcNode) (ton.APIClientWrapped, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), tonScannerConnectTimeout)
	defer cancel()
	return service.ConnectTonLiteAPI(ctx, node.Url, tonScannerRequestTimeout, 5)
}

func recordTonLiteNodeFailure(node mdb.RpcNode, reason string) {
	data.RecordRpcFailure(mdb.NetworkTon)
	failures, cooling := data.RecordRpcNodeFailure(node.ID)
	nodeLabel := data.RpcNodeLogLabel(node)
	if cooling {
		log.Sugar.Warnf("[TON] lite node reached fail threshold (%s), node=%s failures=%d/%d", reason, nodeLabel, failures, data.RpcFailoverThreshold)
		return
	}
	log.Sugar.Warnf("[TON] lite node failed (%s), node=%s failures=%d/%d", reason, nodeLabel, failures, data.RpcFailoverThreshold)
}
