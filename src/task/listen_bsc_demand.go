package task

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	bscOrderScanGrace     = 10 * time.Minute
	bscOrderScanLead      = 2 * time.Minute
	bscIdlePollInterval   = 5 * time.Second
	bscIdleHealthInterval = 5 * time.Minute
)

// A WS reconnect wakes the independent scanner. It does not replace periodic
// scans: a live subscription can silently lose events without disconnecting.
var bscBackfillWake = make(chan struct{}, 1)

func wakeBscBackfill(network string) {
	if network == mdb.NetworkBsc {
		select {
		case bscBackfillWake <- struct{}{}:
		default:
		}
	}
}

func waitBscBackfill(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-bscBackfillWake:
		return true
	case <-timer.C:
		return true
	}
}

type bscBackfillProgress struct {
	lastBlock      int64
	coveredThrough time.Time
	loaded         bool
}

// nextBscOrderStart finds the earliest not-yet-covered payment window. We keep
// the cursor while idle and seek only over time with no eligible orders. This
// avoids replaying days of empty blocks when the next customer checks out.
func nextBscOrderStart(orders []mdb.Orders, now time.Time) time.Time {
	start := now.Add(-bscOrderScanLead)
	for _, order := range orders {
		candidate := time.UnixMilli(order.CreatedAt.TimestampMilli()).Add(-bscOrderScanLead)
		if order.CreatedAt.IsZero() {
			return time.Unix(0, 0)
		}
		if candidate.Before(start) {
			start = candidate
		}
	}
	return start
}

type evmHeaderReader func(context.Context, *big.Int) (*types.Header, error)

func checkedBscHeader(ctx context.Context, read evmHeaderReader, block *big.Int) (*types.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, evmBackfillHeaderTimeout)
	defer cancel()
	h, err := read(ctx, block)
	if err != nil {
		return nil, err
	}
	if h == nil || h.Number == nil || !h.Number.IsInt64() || h.Number.Sign() < 0 || h.Time > uint64(1<<63-1) || (h.Time == 0 && h.Number.Sign() != 0) {
		return nil, fmt.Errorf("invalid BSC block header")
	}
	if block != nil && h.Number.Cmp(block) != 0 {
		return nil, fmt.Errorf("BSC header number mismatch")
	}
	return h, nil
}

// Find the last block STRICTLY before the start of an order window. Equal
// timestamps stay in the scan (BSC can have multiple blocks in one second).
// Bounds come from verified headers, never an assumed block production rate.
func bscBlockBefore(ctx context.Context, read evmHeaderReader, low int64, high *types.Header, before time.Time) (*types.Header, error) {
	lo, err := checkedBscHeader(ctx, read, big.NewInt(low))
	if err != nil {
		return nil, err
	}
	if lo.Time > high.Time || lo.Number.Cmp(high.Number) > 0 {
		return nil, fmt.Errorf("inconsistent BSC header bounds")
	}
	cutoff := before.Unix()
	if cutoff <= 0 || int64(lo.Time) >= cutoff {
		return lo, nil
	}
	if int64(high.Time) < cutoff {
		return high, nil
	}
	hi := high
	for hi.Number.Int64()-lo.Number.Int64() > 1 {
		mid := lo.Number.Int64() + (hi.Number.Int64()-lo.Number.Int64())/2
		h, err := checkedBscHeader(ctx, read, big.NewInt(mid))
		if err != nil {
			return nil, err
		}
		if h.Time < lo.Time || h.Time > hi.Time {
			return nil, fmt.Errorf("non-monotonic BSC block timestamps")
		}
		if int64(h.Time) < cutoff {
			lo = h
		} else {
			hi = h
		}
	}
	return lo, nil
}

// runBscDemandBackfillStep runs at most one 10-block log batch. Idle iterations
// read only the local orders DB; a failed query is an error, never "no orders".
func runBscDemandBackfillStep(ctx context.Context, client *ethclient.Client, node mdb.RpcNode, query ethereum.FilterQuery, isWatched evmRecipientCheckerFunc, progress *bscBackfillProgress, now time.Time) (time.Duration, error) {
	chain, interval := evmBackfillChainConfig(mdb.NetworkBsc)
	if chain == nil {
		return 0, fmt.Errorf("load BSC chain config")
	}
	if !chain.Enabled {
		return bscIdlePollInterval, nil
	}
	if !progress.loaded {
		last, initialized, err := loadEvmScanCursor(mdb.NetworkBsc)
		if err != nil {
			return 0, err
		}
		if initialized {
			h, err := checkedBscHeader(ctx, client.HeaderByNumber, big.NewInt(last))
			if err != nil {
				return 0, err
			}
			progress.lastBlock, progress.coveredThrough = last, time.Unix(int64(h.Time), 0)
		}
		progress.loaded = true
	}
	orders, err := data.ListEvmBackfillOrders(mdb.NetworkBsc, progress.coveredThrough, config.GetOrderExpirationTimeDuration(), bscOrderScanGrace)
	if err != nil {
		return 0, fmt.Errorf("load BSC scan demand: %w", err)
	}
	if len(orders) == 0 {
		return bscIdlePollInterval, nil
	}
	start := nextBscOrderStart(orders, now)
	head, err := checkedBscHeader(ctx, client.HeaderByNumber, nil)
	if err != nil {
		return 0, err
	}
	confirmed := confirmedEvmHead(head.Number.Int64(), chain.MinConfirmations)
	if confirmed < progress.lastBlock {
		return 0, fmt.Errorf("BSC node behind saved scan cursor")
	}
	if confirmed == progress.lastBlock {
		return interval, nil
	}
	if confirmed != head.Number.Int64() {
		head, err = checkedBscHeader(ctx, client.HeaderByNumber, big.NewInt(confirmed))
		if err != nil {
			return 0, err
		}
	}
	if start.After(progress.coveredThrough) {
		skip, err := bscBlockBefore(ctx, client.HeaderByNumber, progress.lastBlock, head, start)
		if err != nil {
			return 0, err
		}
		// Seek is deliberately not persisted separately: only a successfully
		// queried batch commits the new coverage. Errors/restarts retry it.
		if skip.Number.Int64() > progress.lastBlock {
			copy := *progress
			copy.lastBlock, copy.coveredThrough = skip.Number.Int64(), time.Unix(int64(skip.Time), 0)
			return scanBscDemandBatch(ctx, client, node, query, isWatched, progress, copy, head, interval)
		}
	}
	return scanBscDemandBatch(ctx, client, node, query, isWatched, progress, *progress, head, interval)
}

func scanBscDemandBatch(ctx context.Context, client *ethclient.Client, node mdb.RpcNode, query ethereum.FilterQuery, isWatched evmRecipientCheckerFunc, saved *bscBackfillProgress, next bscBackfillProgress, head *types.Header, interval time.Duration) (time.Duration, error) {
	if next.lastBlock >= head.Number.Int64() {
		return interval, nil
	}
	to := next.lastBlock + evmBackfillBatchSize(mdb.NetworkBsc)
	if to > head.Number.Int64() {
		to = head.Number.Int64()
	}
	end := head
	var err error
	if to != head.Number.Int64() {
		end, err = checkedBscHeader(ctx, client.HeaderByNumber, big.NewInt(to))
		if err != nil {
			return 0, err
		}
	}
	if time.Unix(int64(end.Time), 0).Before(next.coveredThrough) {
		return 0, fmt.Errorf("BSC scan timestamp moved backwards")
	}
	recipients := loadEvmRecipientTopics(mdb.NetworkBsc, "[BSC-BACKFILL]")
	if len(recipients) == 0 {
		return bscIdlePollInterval, nil
	}
	query.FromBlock, query.ToBlock = big.NewInt(next.lastBlock+1), big.NewInt(to)
	query.Topics = evmTransferTopics(recipients)
	rpcCtx, cancel := context.WithTimeout(ctx, evmBackfillQueryTimeout)
	logs, err := client.FilterLogs(rpcCtx, query)
	cancel()
	if err != nil {
		return 0, fmt.Errorf("BSC filter logs range=%d-%d: %w", next.lastBlock+1, to, err)
	}
	if err = processEvmBackfillLogs(ctx, client, mdb.NetworkBsc, "[BSC-BACKFILL]", logs, isWatched); err != nil {
		return 0, err
	}
	if err = data.UpsertEvmScanCursor(mdb.NetworkBsc, to); err != nil {
		return 0, err
	}
	next.lastBlock, next.coveredThrough = to, time.Unix(int64(end.Time), 0)
	*saved = next
	data.RecordRpcBlockHeight(mdb.NetworkBsc, head.Number.Int64())
	data.RecordRpcSuccess(mdb.NetworkBsc)
	data.RecordRpcNodeSuccess(node.ID)
	if to < head.Number.Int64() {
		return evmBackfillCatchupDelay, nil
	}
	return interval, nil
}

func runBscDemandBackfillLoop(ctx context.Context, client *ethclient.Client, node mdb.RpcNode, query ethereum.FilterQuery, isWatched evmRecipientCheckerFunc) error {
	var progress bscBackfillProgress
	for ctx.Err() == nil {
		delay, err := runBscDemandBackfillStep(ctx, client, node, query, isWatched, &progress, time.Now())
		if err != nil {
			return err
		}
		if !waitBscBackfill(ctx, delay) {
			return nil
		}
	}
	return nil
}
