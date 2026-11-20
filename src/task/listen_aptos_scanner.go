package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
)

const (
	aptosLedgerChunkSize   int64 = 100
	aptosLedgerWorkerCount       = 3
	aptosLedgerQueueLimit        = 100
)

type aptosRuntimeCursor struct {
	initialized     bool
	lastSeenVersion int64
}

type aptosLedgerRange struct {
	start int64
	limit int64
	state moveWatchState
}

type aptosChainProvider interface {
	LatestLedgerVersion() (int64, error)
	Transactions(start int64, limit int64) ([]byte, error)
}

type serviceAptosProvider struct{}

func (serviceAptosProvider) LatestLedgerVersion() (int64, error) {
	return service.AptosGetLedgerVersion()
}

func (serviceAptosProvider) Transactions(start int64, limit int64) ([]byte, error) {
	return service.AptosGetTransactions(start, limit)
}

var (
	parseAptosTransfersForWallets    = service.ParseAptosTransfersForWallets
	processAptosObservedTransferFunc = service.ProcessMoveObservedTransferResult
)

func StartAptosLedgerScannerListener() {
	provider := serviceAptosProvider{}
	cursor := &aptosRuntimeCursor{}
	log.Sugar.Infof("[APTOS] ledger scanner starting mode=full_ledger chunk_size=%d workers=%d queue_limit=%d",
		aptosLedgerChunkSize,
		aptosLedgerWorkerCount,
		aptosLedgerQueueLimit,
	)
	for {
		if err := runAptosLedgerScanner(context.Background(), provider, cursor); err != nil {
			log.Sugar.Warnf("[APTOS] scanner stopped: %v", err)
			sleepOrDone(context.Background(), moveScannerRetryDelay)
			continue
		}
	}
}

func runAptosLedgerScanner(ctx context.Context, provider aptosChainProvider, cursor *aptosRuntimeCursor) error {
	if cursor == nil {
		cursor = &aptosRuntimeCursor{}
	}
	watchSignature := ""
	for {
		chain, interval := moveChainConfig(mdb.NetworkAptos)
		if chain == nil || !chain.Enabled {
			log.Sugar.Debug("[APTOS] chain disabled or not configured, idling")
			if !sleepOrDone(ctx, interval) {
				return nil
			}
			continue
		}
		state, err := loadMoveWatchState(mdb.NetworkAptos)
		if err != nil {
			return err
		}
		if len(state.wallets) == 0 || len(state.tokens) == 0 {
			log.Sugar.Debug("[APTOS] no enabled wallets or tokens, idling")
			if !sleepOrDone(ctx, interval) {
				return nil
			}
			continue
		}
		if sig := moveWatchStateSignature(state); sig != watchSignature {
			watchSignature = sig
			log.Sugar.Infof("[APTOS] ledger scanner watching wallets=%d tokens=%d", len(state.wallets), len(state.tokens))
		}

		latest, err := provider.LatestLedgerVersion()
		if err != nil {
			return fmt.Errorf("get latest ledger version: %w", err)
		}
		data.RecordRpcBlockHeight(mdb.NetworkAptos, latest)
		confirmed := moveConfirmedCursor(latest, chain.MinConfirmations)
		if !cursor.initialized {
			cursor.initialized = true
			cursor.lastSeenVersion = confirmed - aptosLedgerChunkSize
			if cursor.lastSeenVersion < -1 {
				cursor.lastSeenVersion = -1
			}
			log.Sugar.Infof(
				"[APTOS] initialized ledger cursor at version=%d confirmed_head=%d chunk_size=%d mode=full_ledger_watch",
				cursor.lastSeenVersion,
				confirmed,
				aptosLedgerChunkSize,
			)
		}

		catchup, err := processAptosLedgerRound(ctx, provider, state, cursor, confirmed)
		if err != nil {
			return err
		}
		if !sleepOrDone(ctx, chooseAptosIdleDelay(catchup, interval)) {
			return nil
		}
	}
}

func processAptosLedgerRound(ctx context.Context, provider aptosChainProvider, state moveWatchState, cursor *aptosRuntimeCursor, confirmedVersion int64) (bool, error) {
	ranges := buildAptosLedgerRanges(cursor.lastSeenVersion, confirmedVersion, state)
	if len(ranges) == 0 {
		return false, nil
	}
	lastRange := ranges[len(ranges)-1]
	log.Sugar.Infof(
		"[APTOS] queued ledger ranges count=%d from=%d to=%d cursor=%d confirmed_head=%d workers=%d",
		len(ranges),
		ranges[0].start,
		lastRange.start+lastRange.limit-1,
		cursor.lastSeenVersion,
		confirmedVersion,
		aptosLedgerWorkerCount,
	)

	type rangeResult struct {
		item aptosLedgerRange
		err  error
	}
	jobs := make(chan aptosLedgerRange, len(ranges))
	results := make(chan rangeResult, len(ranges))
	workers := aptosLedgerWorkerCount
	if len(ranges) < workers {
		workers = len(ranges)
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				results <- rangeResult{item: item, err: processAptosLedgerRange(ctx, provider, item, confirmedVersion)}
			}
		}()
	}
	for _, item := range ranges {
		jobs <- item
	}
	close(jobs)
	wg.Wait()
	close(results)

	errByStart := make(map[int64]error, len(ranges))
	for result := range results {
		if result.err != nil {
			errByStart[result.item.start] = result.err
		}
	}

	for _, item := range ranges {
		if err := errByStart[item.start]; err != nil {
			return cursor.lastSeenVersion < confirmedVersion, err
		}
		cursor.lastSeenVersion = item.start + item.limit - 1
	}
	return cursor.lastSeenVersion < confirmedVersion, nil
}

func buildAptosLedgerRanges(lastSeenVersion int64, confirmedVersion int64, state moveWatchState) []aptosLedgerRange {
	if confirmedVersion <= lastSeenVersion {
		return nil
	}
	ranges := make([]aptosLedgerRange, 0, aptosLedgerQueueLimit)
	next := lastSeenVersion + 1
	if next < 0 {
		next = 0
	}
	for next <= confirmedVersion && len(ranges) < aptosLedgerQueueLimit {
		limit := confirmedVersion - next + 1
		if limit > aptosLedgerChunkSize {
			limit = aptosLedgerChunkSize
		}
		ranges = append(ranges, aptosLedgerRange{start: next, limit: limit, state: state})
		next += limit
	}
	return ranges
}

func processAptosLedgerRange(ctx context.Context, provider aptosChainProvider, item aptosLedgerRange, confirmedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := provider.Transactions(item.start, item.limit)
	if err != nil {
		return fmt.Errorf("get transactions start=%d limit=%d: %w", item.start, item.limit, err)
	}
	transfers, err := parseAptosTransfersForWallets(body, item.state.wallets, item.state.tokens)
	if err != nil {
		return fmt.Errorf("parse transactions start=%d limit=%d: %w", item.start, item.limit, err)
	}
	for _, transfer := range transfers {
		log.Sugar.Infof(
			"[APTOS] observed transfer version=%d tx=%s token=%s amount=%.8f raw=%s decimals=%d receive=%s key=%s",
			transfer.Version,
			transfer.TxID,
			transfer.Token,
			transfer.Amount,
			transfer.RawAmount.String(),
			transfer.Decimals,
			transfer.ReceiveAddress,
			transfer.TransferKey,
		)
		if err = processAptosObservedTransferFunc(transfer); err != nil {
			return err
		}
	}
	if len(transfers) > 0 {
		log.Sugar.Infof(
			"[APTOS] ledger range processed start=%d limit=%d to=%d transfers=%d confirmed_head=%d",
			item.start,
			item.limit,
			item.start+item.limit-1,
			len(transfers),
			confirmedVersion,
		)
	}
	return nil
}

func chooseAptosIdleDelay(catchup bool, interval time.Duration) time.Duration {
	if catchup {
		return moveCatchupYield
	}
	return interval
}
