package task

import (
	"context"
	"fmt"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	evmDialClient = func(rawURL string) (*ethclient.Client, error) {
		return ethclient.Dial(rawURL)
	}
	evmSubscribeLogs = func(ctx context.Context, client *ethclient.Client, query ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
		return client.SubscribeFilterLogs(ctx, query, ch)
	}
)

// runEvmWsLogListener connects to node.Url, subscribes to Transfer logs,
// and dispatches each log to handleLog. It retries on transient errors
// with exponential backoff until the node reaches the failure threshold.
// The ctx lets the caller trigger a clean exit — e.g. when admin disables
// the chain, the caller cancels the context and the function returns.
func runEvmWsLogListener(ctx context.Context, logPrefix, network string, node mdb.RpcNode, query ethereum.FilterQuery, handleLog func(*ethclient.Client, types.Log)) {
	const (
		minBackoff       = 2 * time.Second
		maxBackoff       = 60 * time.Second
		rejoinWait       = 3 * time.Second
		stableResetAfter = 60 * time.Second
		logBufferSize    = 256
	)
	failWait := minBackoff
	wsURL := node.Url
	nodeLabel := data.RpcNodeLogLabel(node)

	for {
		if ctx.Err() != nil {
			return
		}

		client, err := evmDialClient(wsURL)
		if err != nil {
			log.Sugar.Warnf("%s dial: %v, retry in %s", logPrefix, err, failWait)
			if recordEvmWsNodeFailure(logPrefix, node, "dial") {
				return
			}
			if !sleepOrDone(ctx, failWait) {
				return
			}
			failWait = nextBackoff(failWait, maxBackoff)
			continue
		}

		logsCh := make(chan types.Log, logBufferSize)
		sub, err := evmSubscribeLogs(ctx, client, query, logsCh)
		if err != nil {
			client.Close()
			log.Sugar.Warnf("%s subscribe: %v, retry in %s", logPrefix, err, failWait)
			if recordEvmWsNodeFailure(logPrefix, node, "subscribe") {
				return
			}
			if !sleepOrDone(ctx, failWait) {
				return
			}
			failWait = nextBackoff(failWait, maxBackoff)
			continue
		}

		snapshotBlock, err := backfillEvmLogs(ctx, client, logPrefix, network, query, handleLog)
		if err != nil {
			sub.Unsubscribe()
			client.Close()
			log.Sugar.Warnf("%s backfill: %v, retry in %s", logPrefix, err, failWait)
			if recordEvmWsNodeFailure(logPrefix, node, "backfill") {
				return
			}
			if !sleepOrDone(ctx, failWait) {
				return
			}
			failWait = nextBackoff(failWait, maxBackoff)
			continue
		}
		failWait = minBackoff

		log.Sugar.Infof("%s connected, subscribed to Transfer logs using WSS node %s (backfilled to block %d)", logPrefix, nodeLabel, snapshotBlock)

		connectedAt := time.Now()
		lastCursor := snapshotBlock
		recvErr := recvLoop(ctx, client, sub, logsCh, logPrefix, func(client *ethclient.Client, vLog types.Log) {
			if vLog.Removed {
				return
			}
			handleLog(client, vLog)
			if vLog.BlockNumber <= lastCursor {
				return
			}
			if err := saveEvmBackfillCursor(network, vLog.BlockNumber); err != nil {
				log.Sugar.Warnf("%s save backfill cursor block=%d: %v", logPrefix, vLog.BlockNumber, err)
				return
			}
			lastCursor = vLog.BlockNumber
		})

		if ctx.Err() != nil {
			return
		}
		if time.Since(connectedAt) >= stableResetAfter {
			data.RecordRpcNodeSuccess(node.ID)
		} else if recvErr != nil && recordEvmWsNodeFailure(logPrefix, node, recvErr.Error()) {
			return
		}
		if !sleepOrDone(ctx, rejoinWait) {
			return
		}
	}
}

func recvLoop(ctx context.Context, client *ethclient.Client, sub ethereum.Subscription, logsCh <-chan types.Log, logPrefix string, handleLog func(*ethclient.Client, types.Log)) error {
	defer func() {
		sub.Unsubscribe()
		client.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			log.Sugar.Infof("%s context cancelled, stopping", logPrefix)
			return nil
		case err := <-sub.Err():
			if err != nil {
				log.Sugar.Warnf("%s subscription error: %v, reconnecting", logPrefix, err)
				return fmt.Errorf("subscription error")
			} else {
				log.Sugar.Warnf("%s subscription closed, reconnecting", logPrefix)
				return fmt.Errorf("subscription closed")
			}
		case vLog, ok := <-logsCh:
			if !ok {
				log.Sugar.Warnf("%s log channel closed, reconnecting", logPrefix)
				return fmt.Errorf("log channel closed")
			}
			handleLog(client, vLog)
		}
	}
}

func recordEvmWsNodeFailure(logPrefix string, node mdb.RpcNode, reason string) bool {
	failures, cooling := data.RecordRpcNodeFailure(node.ID)
	nodeLabel := data.RpcNodeLogLabel(node)
	if !cooling {
		log.Sugar.Warnf("%s WSS node failed (%s), node=%s failures=%d/%d", logPrefix, reason, nodeLabel, failures, data.RpcFailoverThreshold)
		return false
	}
	log.Sugar.Warnf("%s WSS node reached fail threshold (%s), node=%s, resolving another node", logPrefix, reason, nodeLabel)
	return true
}

// sleepOrDone waits for d or for ctx cancellation, whichever comes
// first. Returns true if the sleep completed normally, false if ctx
// was cancelled (caller should exit).
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	n := cur * 2
	if n > max {
		return max
	}
	return n
}
