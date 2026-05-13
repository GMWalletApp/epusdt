package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/GMWalletApp/epusdt/util/math"
	"github.com/shopspring/decimal"
	tonaddress "github.com/xssnick/tonutils-go/address"
	"github.com/xssnick/tonutils-go/liteclient"
	"github.com/xssnick/tonutils-go/tlb"
	"github.com/xssnick/tonutils-go/ton"
	"github.com/xssnick/tonutils-go/ton/jetton"
	"github.com/xssnick/tonutils-go/tvm/cell"
)

// Retryable outcomes MUST NOT be cached: caching a transient failure
// turns it into a multi-hour blind spot that outlasts the order.
type tonOutcome int

const (
	tonOutcomeProcessed tonOutcome = iota
	tonOutcomeIrrelevant
	tonOutcomeRetry
)

// Body opcodes we accept as plain wallet-to-wallet transfers. Anything
// else is a contract message whose attached TON is forwarding gas.
const (
	tonOpTextComment      uint64 = 0x00000000
	tonOpEncryptedComment uint64 = 0x2167da4b // TEP-1 encrypted comment (Tonkeeper-style)
)

// Block-time is used ONLY to size auxiliary windows (TTL, cutoff).
// Confirmation depth itself is counted via real master seqno advance.
const (
	tonBlockTimeSeconds             = 5
	tonListLimit                    = 32
	tonRequestTimeout               = 30 * time.Second
	tonProcessedCacheTTL            = 1 * time.Hour
	tonNativeDecimals               = 9
	tonPendingFloorTTL              = int64(3600) // 1h floor for pending TTL
	tonPendingTTLSafetyMultiplier   = int64(3)
	tonScanCutoffMaxExtraSeconds    = int64(3600) // 1h cap on scan extension
	tonMaxEffectiveMinConfirmations = int(tonScanCutoffMaxExtraSeconds / tonBlockTimeSeconds)
)

var (
	gTonAPIMu        sync.Mutex
	gTonAPI          ton.APIClientWrapped
	gTonAPIConfigURL string

	gProcessedTonTx    sync.Map // hex(hash) -> unix ts
	gTonPendingConfirm sync.Map // hex(hash) -> tonPendingEntry
	gTonJettonWalletMu sync.Mutex
	gTonJettonWallet   = map[string]*tonaddress.Address{} // owner|master -> jetton wallet
)

// firstSeenMasterSeqno is a safe upper bound on the tx's actual
// anchor block. We don't estimate a head-start from tx.Now because
// wall-clock and block-time estimates can over-credit. expiresAt is
// per-entry because admin-tunable min_confirmations may need head-room
// past the 1h floor.
type tonPendingEntry struct {
	firstSeenMasterSeqno uint32
	expiresAt            int64 // unix
}

// Clamps the admin-configured min_confirmations to a value the listener
// can satisfy within its scan window. <=0 disables the gate. Values
// above the supported max are clamped with a warn so misconfig yields
// degraded depth rather than stranded payments. The lock-TTL extension
// at order creation uses the same clamped value for consistency.
func effectiveTonMinConfirmations(configured int) int {
	if configured <= 0 {
		return 0
	}
	if configured > tonMaxEffectiveMinConfirmations {
		log.Sugar.Warnf("[TON] chains.min_confirmations=%d exceeds listener max=%d; clamping",
			configured, tonMaxEffectiveMinConfirmations)
		return tonMaxEffectiveMinConfirmations
	}
	return configured
}

func loadTonMinConfirmations() int {
	configured := 1 // defence-in-depth when admin hasn't tuned the seed
	if chainRow, err := data.GetChainByNetwork(mdb.NetworkTon); err == nil && chainRow != nil && chainRow.MinConfirmations > 0 {
		configured = chainRow.MinConfirmations
	}
	return effectiveTonMinConfirmations(configured)
}

// Extra time a TON lock must outlive order_expiration so the
// confirmation gate has room to settle payments arriving near expiry.
func tonLockExpirationBuffer() time.Duration {
	return time.Duration(loadTonMinConfirmations()*tonBlockTimeSeconds) * time.Second
}

// TonCallBack scans a single owner address for inbound payments and
// confirms any matching pending orders. Errors are logged; the next
// tick retries.
func TonCallBack(ownerAddrStr string, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		if err := recover(); err != nil {
			log.Sugar.Errorf("[TON][%s] panic recovered: %v", ownerAddrStr, err)
		}
	}()

	api, err := ensureTonAPI()
	if err != nil {
		log.Sugar.Errorf("[TON][%s] init api err=%v", ownerAddrStr, err)
		return
	}

	ownerAddr, err := tonaddress.ParseAddr(ownerAddrStr)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] parse owner addr err=%v", ownerAddrStr, err)
		return
	}

	tokens, err := data.ListEnabledChainTokensByNetwork(mdb.NetworkTon)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] load chain_tokens err=%v", ownerAddrStr, err)
		return
	}
	if len(tokens) == 0 {
		log.Sugar.Debugf("[TON][%s] no enabled chain_tokens, skipping", ownerAddrStr)
		return
	}

	minConfirmations := loadTonMinConfirmations()

	// Partition tokens: symbol=TON with empty contract → native; non-empty
	// contract → Jetton master.
	var nativeToken *mdb.ChainToken
	jettonTokens := make(map[string]*mdb.ChainToken, len(tokens))
	for i := range tokens {
		sym := strings.ToUpper(strings.TrimSpace(tokens[i].Symbol))
		contract := strings.TrimSpace(tokens[i].ContractAddress)
		if sym == "TON" && contract == "" {
			nativeToken = &tokens[i]
			continue
		}
		if contract == "" {
			continue
		}
		jettonTokens[contract] = &tokens[i]
	}

	cleanupTonProcessedCache()
	cleanupTonPendingConfirm()

	ctx, cancel := context.WithTimeout(context.Background(), tonRequestTimeout)
	defer cancel()

	master, err := api.CurrentMasterchainInfo(ctx)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] masterchain info err=%v", ownerAddrStr, err)
		return
	}

	// Jetton wallet address is deterministic per (master, owner); cache.
	jettonWalletToToken := make(map[string]*mdb.ChainToken, len(jettonTokens))
	for masterStr, tk := range jettonTokens {
		jwAddr, err := resolveJettonWalletAddress(ctx, api, master, ownerAddr, masterStr)
		if err != nil {
			log.Sugar.Errorf("[TON][%s] resolve jetton wallet master=%s err=%v", ownerAddrStr, masterStr, err)
			continue
		}
		jettonWalletToToken[jwAddr.StringRaw()] = tk
	}

	acc, err := api.GetAccount(ctx, master, ownerAddr)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] get account err=%v", ownerAddrStr, err)
		return
	}
	if !acc.IsActive || acc.LastTxLT == 0 {
		log.Sugar.Debugf("[TON][%s] account not active or no transactions yet", ownerAddrStr)
		return
	}

	// Cutoff covers the gate's max wait so high min_confirmations don't
	// age out before settling.
	extraCutoff := int64(minConfirmations) * tonBlockTimeSeconds
	cutoff := time.Now().
		Add(-config.GetOrderExpirationTimeDuration() - 5*time.Minute - time.Duration(extraCutoff)*time.Second).
		Unix()
	lt := acc.LastTxLT
	txHash := acc.LastTxHash
	scanned := 0
	currentMasterSeqno := master.SeqNo

	for lt > 0 && len(txHash) > 0 {
		txs, err := api.ListTransactions(ctx, ownerAddr, tonListLimit, lt, txHash)
		if err != nil {
			if errors.Is(err, ton.ErrNoTransactionsWereFound) {
				break
			}
			log.Sugar.Errorf("[TON][%s] list transactions err=%v", ownerAddrStr, err)
			return
		}
		if len(txs) == 0 {
			break
		}

		// ListTransactions returns oldest-first; walk newest-first.
		for i := len(txs) - 1; i >= 0; i-- {
			tx := txs[i]
			scanned++

			if int64(tx.Now) < cutoff {
				log.Sugar.Debugf("[TON][%s] tx now=%d before cutoff=%d, stopping scan", ownerAddrStr, tx.Now, cutoff)
				return
			}

			if len(tx.Hash) == 0 {
				log.Sugar.Warnf("[TON][%s] skipping tx with empty hash lt=%d", ownerAddrStr, tx.LT)
				continue
			}
			hashKey := hex.EncodeToString(tx.Hash)
			if _, dup := gProcessedTonTx.Load(hashKey); dup {
				continue
			}

			if !tonConfirmationDepthReached(hashKey, currentMasterSeqno, minConfirmations) {
				continue
			}

			outcome := processTonTransaction(tx, ownerAddrStr, nativeToken, jettonWalletToToken)
			// Only cache terminal outcomes; retryable ones get another tick.
			if outcome != tonOutcomeRetry {
				gProcessedTonTx.Store(hashKey, time.Now().Unix())
				gTonPendingConfirm.Delete(hashKey)
			}
		}

		oldest := txs[0]
		if oldest.PrevTxLT == 0 || len(oldest.PrevTxHash) == 0 {
			break
		}
		lt = oldest.PrevTxLT
		txHash = oldest.PrevTxHash
	}

	log.Sugar.Debugf("[TON][%s] scan complete, processed %d transactions", ownerAddrStr, scanned)
}

// Inspects an incoming internal message and matches it against active
// orders. Confirmation gating is the caller's responsibility.
func processTonTransaction(tx *tlb.Transaction, ownerAddrStr string,
	nativeToken *mdb.ChainToken, jettonWalletToToken map[string]*mdb.ChainToken) tonOutcome {
	if tx.IO.In == nil || tx.IO.In.MsgType != tlb.MsgTypeInternal {
		return tonOutcomeIrrelevant
	}
	ti := tx.IO.In.AsInternal()
	if ti == nil {
		return tonOutcomeIrrelevant
	}

	// Skip bounced messages (refunds, not income).
	if dsc, ok := tx.Description.(tlb.TransactionDescriptionOrdinary); ok && dsc.BouncePhase != nil {
		if _, bounced := dsc.BouncePhase.Phase.(tlb.BouncePhaseOk); bounced {
			return tonOutcomeIrrelevant
		}
	}

	txHashHex := hex.EncodeToString(tx.Hash)
	blockTime := int64(ti.CreatedAt)
	if blockTime <= 0 {
		blockTime = int64(tx.Now)
	}

	// Jetton transfer: source is our own jetton wallet contract; body
	// is a transfer_notification.
	if ti.SrcAddr != nil {
		if tk := jettonWalletToToken[ti.SrcAddr.StringRaw()]; tk != nil {
			var notif jetton.TransferNotification
			if err := tlb.LoadFromCell(&notif, ti.Body.BeginParse()); err != nil {
				log.Sugar.Warnf("[TON][%s] tx=%s decode jetton transfer err=%v", ownerAddrStr, txHashHex, err)
				return tonOutcomeIrrelevant
			}
			amount := adjustTonAmount(notif.Amount.Nano(), tk.Decimals)
			senderStr := ""
			if notif.Sender != nil {
				senderStr = notif.Sender.String()
			}
			return matchAndConfirmTonPayment(ownerAddrStr, txHashHex, blockTime, tk, amount, senderStr)
		}
	}

	// Native TON: require non-zero value AND a plain-transfer body shape.
	// Contract messages carrying forwarding TON would otherwise be
	// misclassified as payments on amount collision.
	if nativeToken == nil {
		return tonOutcomeIrrelevant
	}
	nano := ti.Amount.Nano()
	if nano == nil || nano.Sign() <= 0 {
		return tonOutcomeIrrelevant
	}
	if !isPlainTonTransferBody(ti.Body) {
		log.Sugar.Debugf("[TON][%s] tx=%s rejecting non-plain body for native classification", ownerAddrStr, txHashHex)
		return tonOutcomeIrrelevant
	}
	amount := adjustTonAmount(nano, tonNativeDecimals)
	senderStr := ""
	if ti.SrcAddr != nil {
		senderStr = ti.SrcAddr.String()
	}
	return matchAndConfirmTonPayment(ownerAddrStr, txHashHex, blockTime, nativeToken, amount, senderStr)
}

// Block-count confirmation gate via real master seqno advance.
// First sight records currentMasterSeqno as a safe upper bound on
// the tx's anchor and defers; later sights settle once the live
// master seqno has advanced by min_confirmations beyond that.
func tonConfirmationDepthReached(hashKey string, currentMasterSeqno uint32, minConfirmations int) bool {
	if minConfirmations <= 0 {
		return true
	}
	if v, loaded := gTonPendingConfirm.Load(hashKey); loaded {
		entry := v.(tonPendingEntry)
		if currentMasterSeqno <= entry.firstSeenMasterSeqno {
			return false
		}
		return currentMasterSeqno-entry.firstSeenMasterSeqno >= uint32(minConfirmations)
	}
	gTonPendingConfirm.Store(hashKey, tonPendingEntry{
		firstSeenMasterSeqno: currentMasterSeqno,
		expiresAt:            time.Now().Unix() + tonPendingTTLSeconds(minConfirmations),
	})
	return false
}

// Scales with min_confirmations so large admin values have head-room
// to satisfy before eviction; floored at 1h for small values.
func tonPendingTTLSeconds(minConfirmations int) int64 {
	scaled := int64(minConfirmations) * tonBlockTimeSeconds * tonPendingTTLSafetyMultiplier
	if scaled > tonPendingFloorTTL {
		return scaled
	}
	return tonPendingFloorTTL
}

func cleanupTonPendingConfirm() {
	now := time.Now().Unix()
	gTonPendingConfirm.Range(func(k, v interface{}) bool {
		if e, ok := v.(tonPendingEntry); ok && now > e.expiresAt {
			gTonPendingConfirm.Delete(k)
		}
		return true
	})
}

// Plain wallet-to-wallet body: nil/empty, or 32-bit opcode is
// tonOpTextComment / tonOpEncryptedComment. Any other opcode is a
// contract message; treating it as native TON would risk crediting
// a payment from a coincidental amount collision.
func isPlainTonTransferBody(body *cell.Cell) bool {
	if body == nil {
		return true
	}
	loader := body.BeginParse()
	if loader == nil || loader.BitsLeft() < 32 {
		return true
	}
	op, err := loader.LoadUInt(32)
	if err != nil {
		return true
	}
	return op == tonOpTextComment || op == tonOpEncryptedComment
}

func matchAndConfirmTonPayment(ownerAddrStr, txHashHex string, blockTime int64,
	token *mdb.ChainToken, amount float64, sender string) tonOutcome {
	symbol := strings.ToUpper(strings.TrimSpace(token.Symbol))
	if amount <= 0 {
		return tonOutcomeIrrelevant
	}
	if token.MinAmount > 0 && amount < token.MinAmount {
		return tonOutcomeIrrelevant
	}

	log.Sugar.Infof("[TON][%s] tx=%s incoming %s amount=%.6f from=%s -> matching",
		ownerAddrStr, txHashHex, symbol, amount, sender)

	tradeID, err := data.GetTradeIdByWalletAddressAndAmountAndToken(mdb.NetworkTon, ownerAddrStr, symbol, amount)
	if err != nil {
		// Transient DB hiccup — don't poison the dedup cache.
		log.Sugar.Errorf("[TON][%s] tx=%s lock lookup err=%v", ownerAddrStr, txHashHex, err)
		return tonOutcomeRetry
	}
	if tradeID == "" {
		// Order may still be creating; stay retryable until cutoff.
		// Debug-level so an active wallet doesn't flood logs.
		log.Sugar.Debugf("[TON][%s] tx=%s no active transaction_lock: token=%s amount=%.6f",
			ownerAddrStr, txHashHex, symbol, amount)
		return tonOutcomeRetry
	}

	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Errorf("[TON][%s] tx=%s load order err=%v", ownerAddrStr, txHashHex, err)
		return tonOutcomeRetry
	}

	// Reject payments older than the order itself.
	createdMs := order.CreatedAt.TimestampMilli()
	blockMs := blockTime * 1000
	if blockMs < createdMs {
		log.Sugar.Warnf("[TON][%s] tx=%s skipped: block_time_ms=%d before order_created_ms=%d",
			ownerAddrStr, txHashHex, blockMs, createdMs)
		return tonOutcomeIrrelevant
	}

	req := &request.OrderProcessingRequest{
		ReceiveAddress:     ownerAddrStr,
		Token:              symbol,
		Network:            mdb.NetworkTon,
		TradeId:            tradeID,
		Amount:             amount,
		BlockTransactionId: txHashHex,
	}
	if err := OrderProcessing(req); err != nil {
		if errors.Is(err, constant.OrderBlockAlreadyProcess) || errors.Is(err, constant.OrderStatusConflict) {
			log.Sugar.Infof("[TON][%s] tx=%s already resolved: trade_id=%s reason=%v",
				ownerAddrStr, txHashHex, tradeID, err)
			return tonOutcomeProcessed
		}
		log.Sugar.Errorf("[TON][%s] tx=%s OrderProcessing failed trade_id=%s err=%v",
			ownerAddrStr, txHashHex, tradeID, err)
		return tonOutcomeRetry
	}
	log.Sugar.Infof("[TON][%s] order marked paid: trade_id=%s tx=%s token=%s amount=%.6f",
		ownerAddrStr, tradeID, txHashHex, symbol, amount)
	sendPaymentNotification(order)
	return tonOutcomeProcessed
}

func adjustTonAmount(nano *big.Int, decimals int) float64 {
	if nano == nil || nano.Sign() == 0 {
		return 0
	}
	if decimals <= 0 {
		decimals = tonNativeDecimals
	}
	d := decimal.NewFromBigInt(nano, 0)
	divisor := decimal.New(1, int32(decimals))
	adjusted := d.Div(divisor)
	return math.MustParsePrecFloat64(adjusted.InexactFloat64(), data.MaxAmountPrecision)
}

func cleanupTonProcessedCache() {
	cutoff := time.Now().Add(-tonProcessedCacheTTL).Unix()
	gProcessedTonTx.Range(func(k, v interface{}) bool {
		if ts, ok := v.(int64); ok && ts < cutoff {
			gProcessedTonTx.Delete(k)
		}
		return true
	})
}

// Derivation is deterministic per (master, owner) so we cache across
// ticks to avoid repeated get-method round-trips.
func resolveJettonWalletAddress(ctx context.Context, api ton.APIClientWrapped,
	master *ton.BlockIDExt, owner *tonaddress.Address, masterStr string) (*tonaddress.Address, error) {
	masterAddr, err := tonaddress.ParseAddr(masterStr)
	if err != nil {
		if masterAddr, err = tonaddress.ParseRawAddr(masterStr); err != nil {
			return nil, fmt.Errorf("parse master addr %q: %w", masterStr, err)
		}
	}
	key := owner.StringRaw() + "|" + masterAddr.StringRaw()

	gTonJettonWalletMu.Lock()
	cached, ok := gTonJettonWallet[key]
	gTonJettonWalletMu.Unlock()
	if ok && cached != nil {
		return cached, nil
	}

	jc := jetton.NewJettonMasterClient(api, masterAddr)
	wallet, err := jc.GetJettonWalletAtBlock(ctx, owner, master)
	if err != nil {
		return nil, err
	}
	addr := wallet.Address()

	gTonJettonWalletMu.Lock()
	gTonJettonWallet[key] = addr
	gTonJettonWalletMu.Unlock()
	return addr, nil
}

func ensureTonAPI() (ton.APIClientWrapped, error) {
	cfgURL, err := resolveTonConfigURL()
	if err != nil {
		return nil, err
	}

	gTonAPIMu.Lock()
	defer gTonAPIMu.Unlock()
	if gTonAPI != nil && gTonAPIConfigURL == cfgURL {
		return gTonAPI, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), tonRequestTimeout)
	defer cancel()

	cfg, err := liteclient.GetConfigFromUrl(ctx, cfgURL)
	if err != nil {
		return nil, fmt.Errorf("fetch ton config %q: %w", cfgURL, err)
	}

	pool := liteclient.NewConnectionPool()
	if err := pool.AddConnectionsFromConfig(ctx, cfg); err != nil {
		return nil, fmt.Errorf("connect to lite servers: %w", err)
	}

	api := ton.NewAPIClient(pool, ton.ProofCheckPolicyFast).WithRetry()
	api.SetTrustedBlockFromConfig(cfg)

	gTonAPI = api
	gTonAPIConfigURL = cfgURL

	// Cached jetton wallets reference the old pool; drop them.
	gTonJettonWalletMu.Lock()
	gTonJettonWallet = map[string]*tonaddress.Address{}
	gTonJettonWalletMu.Unlock()

	log.Sugar.Infof("[TON] initialized API client from config %s", cfgURL)
	return api, nil
}

func resolveTonConfigURL() (string, error) {
	node, err := data.SelectRpcNode(mdb.NetworkTon, mdb.RpcNodeTypeHttp)
	if err != nil {
		return "", err
	}
	if node == nil || node.ID == 0 {
		return "", fmt.Errorf("no enabled %s %s RPC node configured in rpc_nodes (set the URL of the TON global.config.json)",
			mdb.NetworkTon, mdb.RpcNodeTypeHttp)
	}
	url := strings.TrimSpace(node.Url)
	if url == "" {
		return "", fmt.Errorf("rpc_nodes id=%d has empty url", node.ID)
	}
	return url, nil
}
