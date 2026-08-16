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

type bscRecipientSnapshot struct {
	addrs map[string]struct{}
}

var bscWatchedRecipients atomic.Pointer[bscRecipientSnapshot]

// StartBscWebSocketListener drives the BSC listener. Checks chain
// enable status and reloads contract addresses from chain_tokens every
// 10s so admin-side toggles take effect without a restart.
func StartBscWebSocketListener() {
	for {
		if data.IsChainEnabled(mdb.NetworkBsc) {
			if contracts := loadChainTokenContracts(mdb.NetworkBsc, "[BSC-WS]"); len(contracts) > 0 {
				runBscListener(contracts)
			}
		}
		time.Sleep(10 * time.Second)
	}
}

func runBscListener(contracts []common.Address) {
	ctx, cancel := chainEnabledWatchdog(mdb.NetworkBsc, "[BSC-WS]", chainTokenFingerprint(mdb.NetworkBsc))
	defer cancel()

	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkBsc)
	if err != nil {
		log.Sugar.Errorf("[BSC-WS] Failed to get wallet addresses: %v", err)
		return
	}
	recipientTopics := evmRecipientTopicsFromWallets(wallets)
	if len(recipientTopics) == 0 {
		log.Sugar.Warnf("[BSC-WS] no enabled wallet addresses, listener idle")
		return
	}
	storeBscRecipientsFromWallets(wallets)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkBsc)
				if err != nil {
					log.Sugar.Warnf("[BSC-WS] refresh wallet addresses: %v", err)
					continue
				}
				storeBscRecipientsFromWallets(w)
			}
		}
	}()

	wsNode, ok := resolveChainWsNode(mdb.NetworkBsc, "[BSC-WS]")
	if !ok {
		return
	}
	log.Sugar.Infof("[BSC-WS] connecting using WSS node %s watching %d contract(s)", data.RpcNodeLogLabel(wsNode), len(contracts))

	query := ethereum.FilterQuery{
		Addresses: contracts,
		Topics:    evmTransferTopics(recipientTopics),
	}

	runEvmWsLogListener(ctx, mdb.NetworkBsc, "[BSC-WS]", wsNode, query, func(client *ethclient.Client, vLog types.Log) {
		if len(vLog.Topics) < 3 {
			return
		}

		event := vLog.Topics[0].String()
		if event != transferEventHash.String() {
			return
		}

		amount := new(big.Int).SetBytes(vLog.Data)

		toAddr := common.HexToAddress(vLog.Topics[2].Hex())

		if !isWatchedBscRecipient(toAddr) {
			return
		}

		var blockTsMs int64
		header, err := client.HeaderByNumber(context.Background(), big.NewInt(int64(vLog.BlockNumber)))
		if err != nil {
			data.RecordRpcFailure(mdb.NetworkBsc)
			log.Sugar.Warnf("[BSC-WS] HeaderByNumber block=%d: %v, using local time", vLog.BlockNumber, err)
			blockTsMs = time.Now().UnixMilli()
		} else {
			data.RecordRpcSuccess(mdb.NetworkBsc)
			blockTsMs = int64(header.Time) * 1000
		}

		service.TryProcessEvmERC20Transfer(mdb.NetworkBsc, vLog.Address, toAddr, amount, vLog.TxHash.Hex(), blockTsMs)
	})
}

func storeBscRecipientsFromWallets(wallets []mdb.WalletAddress) int {
	m := make(map[string]struct{})
	for _, w := range wallets {
		a := strings.TrimSpace(w.Address)
		if !common.IsHexAddress(a) {
			continue
		}
		m[strings.ToLower(common.HexToAddress(a).Hex())] = struct{}{}
	}
	bscWatchedRecipients.Store(&bscRecipientSnapshot{addrs: m})
	return len(m)
}

func isWatchedBscRecipient(to common.Address) bool {
	snap := bscWatchedRecipients.Load()
	if snap == nil || len(snap.addrs) == 0 {
		return false
	}
	_, ok := snap.addrs[strings.ToLower(to.Hex())]
	return ok
}
