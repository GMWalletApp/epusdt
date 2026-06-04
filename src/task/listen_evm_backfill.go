package task

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	evmBlockNumber = func(ctx context.Context, client *ethclient.Client) (uint64, error) {
		return client.BlockNumber(ctx)
	}
	evmFilterLogs = func(ctx context.Context, client *ethclient.Client, query ethereum.FilterQuery) ([]types.Log, error) {
		return client.FilterLogs(ctx, query)
	}
	evmBackfillChunkSize         uint64 = 500
	evmBackfillBootstrapLookback uint64 = 128
)

func backfillEvmLogs(ctx context.Context, client *ethclient.Client, logPrefix, network string, baseQuery ethereum.FilterQuery, handleLog func(*ethclient.Client, types.Log)) (uint64, error) {
	latestBlock, err := evmBlockNumber(ctx, client)
	if err != nil {
		return 0, fmt.Errorf("get latest block: %w", err)
	}

	startBlock := evmBackfillStartBlock(network, latestBlock)
	if latestBlock < startBlock {
		startBlock = latestBlock
	}
	if latestBlock > startBlock {
		log.Sugar.Infof("%s backfilling blocks %d-%d", logPrefix, startBlock, latestBlock)
	}

	chunkSize := evmBackfillChunkSize
	if chunkSize == 0 {
		chunkSize = 1
	}

	for from := startBlock; from <= latestBlock; {
		to := from + chunkSize - 1
		if to < from || to > latestBlock {
			to = latestBlock
		}

		query := baseQuery
		query.FromBlock = new(big.Int).SetUint64(from)
		query.ToBlock = new(big.Int).SetUint64(to)

		logs, err := evmFilterLogs(ctx, client, query)
		if err != nil {
			return 0, fmt.Errorf("filter logs %d-%d: %w", from, to, err)
		}
		sort.Slice(logs, func(i, j int) bool {
			if logs[i].BlockNumber != logs[j].BlockNumber {
				return logs[i].BlockNumber < logs[j].BlockNumber
			}
			if logs[i].TxIndex != logs[j].TxIndex {
				return logs[i].TxIndex < logs[j].TxIndex
			}
			return logs[i].Index < logs[j].Index
		})
		for _, vLog := range logs {
			if vLog.Removed {
				continue
			}
			handleLog(client, vLog)
		}
		if err := saveEvmBackfillCursor(network, to); err != nil {
			return 0, fmt.Errorf("save cursor %d: %w", to, err)
		}

		if to == latestBlock {
			break
		}
		from = to + 1
	}

	return latestBlock, nil
}

func evmBackfillStartBlock(network string, latestBlock uint64) uint64 {
	if cursor, ok := loadEvmBackfillCursor(network); ok {
		if cursor > latestBlock {
			return latestBlock
		}
		return cursor
	}
	if evmBackfillBootstrapLookback <= 1 || latestBlock == 0 {
		return latestBlock
	}
	if latestBlock+1 <= evmBackfillBootstrapLookback {
		return 0
	}
	return latestBlock - evmBackfillBootstrapLookback + 1
}

func loadEvmBackfillCursor(network string) (uint64, bool) {
	raw := strings.TrimSpace(data.GetSettingString(evmBackfillCursorSettingKey(network), ""))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func saveEvmBackfillCursor(network string, block uint64) error {
	if current, ok := loadEvmBackfillCursor(network); ok && current >= block {
		return nil
	}
	return data.SetSetting(mdb.SettingGroupSystem, evmBackfillCursorSettingKey(network), strconv.FormatUint(block, 10), mdb.SettingTypeInt)
}

func evmBackfillCursorSettingKey(network string) string {
	return "system.evm_backfill_cursor." + strings.ToLower(strings.TrimSpace(network))
}
