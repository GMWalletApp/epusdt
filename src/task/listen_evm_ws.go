package task

import (
	"context"
	"math/big"
	"time"

	"github.com/assimon/luuu/config"
	"github.com/assimon/luuu/util/log"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

func evmBackfillLookbackBlocks(avgBlockTime time.Duration) uint64 {
	lookback := config.GetOrderExpirationTimeDuration() + 5*time.Minute
	blocks := uint64(lookback / avgBlockTime)
	if lookback%avgBlockTime != 0 {
		blocks++
	}
	blocks *= 2
	if blocks < 256 {
		return 256
	}
	return blocks
}

func backfillEvmLogs(client *ethclient.Client, logPrefix string, query ethereum.FilterQuery, lastSeenBlock *uint64, lookbackBlocks uint64, handleLog func(*ethclient.Client, types.Log)) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		log.Sugar.Warnf("%s latest header for backfill: %v", logPrefix, err)
		return
	}
	latestBlock := header.Number.Uint64()

	startBlock := uint64(0)
	switch {
	case *lastSeenBlock > 0 && *lastSeenBlock < latestBlock:
		startBlock = *lastSeenBlock + 1
	case latestBlock >= lookbackBlocks:
		startBlock = latestBlock - lookbackBlocks + 1
	}
	if startBlock > latestBlock {
		return
	}

	const chunkSize uint64 = 512
	for from := startBlock; from <= latestBlock; from += chunkSize {
		to := from + chunkSize - 1
		if to > latestBlock {
			to = latestBlock
		}
		filterQuery := query
		filterQuery.FromBlock = big.NewInt(int64(from))
		filterQuery.ToBlock = big.NewInt(int64(to))

		logs, err := client.FilterLogs(ctx, filterQuery)
		if err != nil {
			log.Sugar.Warnf("%s backfill logs %d-%d: %v", logPrefix, from, to, err)
			return
		}
		for _, vLog := range logs {
			if vLog.BlockNumber > *lastSeenBlock {
				*lastSeenBlock = vLog.BlockNumber
			}
			handleLog(client, vLog)
		}
	}
	if latestBlock > *lastSeenBlock {
		*lastSeenBlock = latestBlock
	}
}

func runEvmWsLogListener(logPrefix, wsURL string, query ethereum.FilterQuery, lookbackBlocks uint64, handleLog func(*ethclient.Client, types.Log)) {
	const (
		minBackoff = 2 * time.Second
		maxBackoff = 60 * time.Second
		rejoinWait = 3 * time.Second
	)
	failWait := minBackoff
	var lastSeenBlock uint64

	for {
		client, err := ethclient.Dial(wsURL)
		if err != nil {
			log.Sugar.Warnf("%s dial: %v, retry in %s", logPrefix, err, failWait)
			time.Sleep(failWait)
			failWait = nextBackoff(failWait, maxBackoff)
			continue
		}

		logsCh := make(chan types.Log)
		sub, err := client.SubscribeFilterLogs(context.Background(), query, logsCh)
		if err != nil {
			client.Close()
			log.Sugar.Warnf("%s subscribe: %v, retry in %s", logPrefix, err, failWait)
			time.Sleep(failWait)
			failWait = nextBackoff(failWait, maxBackoff)
			continue
		}
		failWait = minBackoff

		log.Sugar.Infof("%s connected, subscribed to USDT/USDC Transfer logs", logPrefix)

		backfillEvmLogs(client, logPrefix, query, &lastSeenBlock, lookbackBlocks, handleLog)

		recvLoop(client, sub, logsCh, logPrefix, &lastSeenBlock, handleLog)

		time.Sleep(rejoinWait)
	}
}

func recvLoop(client *ethclient.Client, sub ethereum.Subscription, logsCh <-chan types.Log, logPrefix string, lastSeenBlock *uint64, handleLog func(*ethclient.Client, types.Log)) {
	defer func() {
		sub.Unsubscribe()
		client.Close()
	}()

	for {
		select {
		case err := <-sub.Err():
			if err != nil {
				log.Sugar.Warnf("%s subscription error: %v, reconnecting", logPrefix, err)
			} else {
				log.Sugar.Warnf("%s subscription closed, reconnecting", logPrefix)
			}
			return
		case vLog, ok := <-logsCh:
			if !ok {
				log.Sugar.Warnf("%s log channel closed, reconnecting", logPrefix)
				return
			}
			if vLog.BlockNumber > *lastSeenBlock {
				*lastSeenBlock = vLog.BlockNumber
			}
			handleLog(client, vLog)
		}
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	n := cur * 2
	if n > max {
		return max
	}
	return n
}
