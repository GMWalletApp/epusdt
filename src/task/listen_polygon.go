package task

import (
	"context"
	"math/big"
	"strings"
	"sync/atomic"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

type polygonRecipientSnapshot struct {
	addrs map[string]struct{}
}

var polygonWatchedRecipients atomic.Pointer[polygonRecipientSnapshot]

// StartPolygonWebSocketListener drives the Polygon listener with
// dynamic chain/token config reload every 10s.
func StartPolygonWebSocketListener() {
	for {
		if data.IsChainEnabled(mdb.NetworkPolygon) {
			if contracts := loadChainTokenContracts(mdb.NetworkPolygon, "[POLYGON-WS]"); len(contracts) > 0 {
				runPolygonListener(contracts)
			}
		}
		time.Sleep(10 * time.Second)
	}
}

func runPolygonListener(contracts []common.Address) {
	ctx, cancel := chainEnabledWatchdog(mdb.NetworkPolygon, "[POLYGON-WS]", chainTokenFingerprint(mdb.NetworkPolygon))
	defer cancel()

	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkPolygon)
	if err != nil {
		log.Sugar.Errorf("[POLYGON-WS] Failed to get wallet addresses: %v", err)
		return
	}
	recipientTopics := evmRecipientTopicsFromWallets(wallets)
	if len(recipientTopics) == 0 {
		log.Sugar.Warnf("[POLYGON-WS] no enabled wallet addresses, listener idle")
		return
	}
	storePolygonRecipientsFromWallets(wallets)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkPolygon)
				if err != nil {
					log.Sugar.Warnf("[POLYGON-WS] refresh wallet addresses: %v", err)
					continue
				}
				storePolygonRecipientsFromWallets(w)
			}
		}
	}()

	wsNode, ok := resolveChainWsNode(mdb.NetworkPolygon, "[POLYGON-WS]")
	if !ok {
		return
	}
	log.Sugar.Infof("[POLYGON-WS] connecting using WSS node %s watching %d contract(s)", data.RpcNodeLogLabel(wsNode), len(contracts))

	query := ethereum.FilterQuery{
		Addresses: contracts,
		Topics:    evmTransferTopics(recipientTopics),
	}

	runEvmWsLogListener(ctx, mdb.NetworkPolygon, "[POLYGON-WS]", wsNode, query, func(client *ethclient.Client, vLog types.Log) {
		if len(vLog.Topics) < 3 {
			return
		}

		event := vLog.Topics[0].String()
		if event != transferEventHash.String() {
			return
		}

		amount := new(big.Int).SetBytes(vLog.Data)

		toAddr := common.HexToAddress(vLog.Topics[2].Hex())

		if !isWatchedPolygonRecipient(toAddr) {
			return
		}

		var blockTsMs int64
		header, err := client.HeaderByNumber(context.Background(), big.NewInt(int64(vLog.BlockNumber)))
		if err != nil {
			data.RecordRpcFailure(mdb.NetworkPolygon)
			log.Sugar.Warnf("[POLYGON-WS] HeaderByNumber block=%d: %v, using local time", vLog.BlockNumber, err)
			blockTsMs = time.Now().UnixMilli()
		} else {
			data.RecordRpcSuccess(mdb.NetworkPolygon)
			blockTsMs = int64(header.Time) * 1000
		}

		service.TryProcessEvmERC20Transfer(mdb.NetworkPolygon, vLog.Address, toAddr, amount, vLog.TxHash.Hex(), blockTsMs)
	})
}

func storePolygonRecipientsFromWallets(wallets []mdb.WalletAddress) int {
	m := make(map[string]struct{})
	for _, w := range wallets {
		a := strings.TrimSpace(w.Address)
		if !common.IsHexAddress(a) {
			continue
		}
		m[strings.ToLower(common.HexToAddress(a).Hex())] = struct{}{}
	}
	polygonWatchedRecipients.Store(&polygonRecipientSnapshot{addrs: m})
	return len(m)
}

func isWatchedPolygonRecipient(to common.Address) bool {
	snap := polygonWatchedRecipients.Load()
	if snap == nil || len(snap.addrs) == 0 {
		return false
	}
	_, ok := snap.addrs[strings.ToLower(to.Hex())]
	return ok
}
