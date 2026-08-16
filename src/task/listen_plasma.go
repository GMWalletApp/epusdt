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

type plasmaRecipientSnapshot struct {
	addrs map[string]struct{}
}

var plasmaWatchedRecipients atomic.Pointer[plasmaRecipientSnapshot]

// StartPlasmaWebSocketListener drives the Plasma listener with dynamic
// chain/token config reload every 10s.
func StartPlasmaWebSocketListener() {
	for {
		if data.IsChainEnabled(mdb.NetworkPlasma) {
			if contracts := loadChainTokenContracts(mdb.NetworkPlasma, "[PLASMA-WS]"); len(contracts) > 0 {
				runPlasmaListener(contracts)
			}
		}
		time.Sleep(10 * time.Second)
	}
}

func runPlasmaListener(contracts []common.Address) {
	ctx, cancel := chainEnabledWatchdog(mdb.NetworkPlasma, "[PLASMA-WS]", chainTokenFingerprint(mdb.NetworkPlasma))
	defer cancel()

	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkPlasma)
	if err != nil {
		log.Sugar.Errorf("[PLASMA-WS] Failed to get wallet addresses: %v", err)
		return
	}
	recipientTopics := evmRecipientTopicsFromWallets(wallets)
	if len(recipientTopics) == 0 {
		log.Sugar.Warnf("[PLASMA-WS] no enabled wallet addresses, listener idle")
		return
	}
	storePlasmaRecipientsFromWallets(wallets)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkPlasma)
				if err != nil {
					log.Sugar.Warnf("[PLASMA-WS] refresh wallet addresses: %v", err)
					continue
				}
				storePlasmaRecipientsFromWallets(w)
			}
		}
	}()

	wsNode, ok := resolveChainWsNode(mdb.NetworkPlasma, "[PLASMA-WS]")
	if !ok {
		return
	}
	log.Sugar.Infof("[PLASMA-WS] connecting using WSS node %s watching %d contract(s)", data.RpcNodeLogLabel(wsNode), len(contracts))

	query := ethereum.FilterQuery{
		Addresses: contracts,
		Topics:    evmTransferTopics(recipientTopics),
	}

	runEvmWsLogListener(ctx, mdb.NetworkPlasma, "[PLASMA-WS]", wsNode, query, func(client *ethclient.Client, vLog types.Log) {
		if len(vLog.Topics) < 3 {
			return
		}

		event := vLog.Topics[0].String()
		if event != transferEventHash.String() {
			return
		}

		amount := new(big.Int).SetBytes(vLog.Data)

		toAddr := common.HexToAddress(vLog.Topics[2].Hex())

		if !isWatchedPlasmaRecipient(toAddr) {
			return
		}

		var blockTsMs int64
		header, err := client.HeaderByNumber(context.Background(), big.NewInt(int64(vLog.BlockNumber)))
		if err != nil {
			data.RecordRpcFailure(mdb.NetworkPlasma)
			log.Sugar.Warnf("[PLASMA-WS] HeaderByNumber block=%d: %v, using local time", vLog.BlockNumber, err)
			blockTsMs = time.Now().UnixMilli()
		} else {
			data.RecordRpcSuccess(mdb.NetworkPlasma)
			blockTsMs = int64(header.Time) * 1000
		}

		service.TryProcessEvmERC20Transfer(mdb.NetworkPlasma, vLog.Address, toAddr, amount, vLog.TxHash.Hex(), blockTsMs)
	})
}

func storePlasmaRecipientsFromWallets(wallets []mdb.WalletAddress) int {
	m := make(map[string]struct{})
	for _, w := range wallets {
		a := strings.TrimSpace(w.Address)
		if !common.IsHexAddress(a) {
			continue
		}
		m[strings.ToLower(common.HexToAddress(a).Hex())] = struct{}{}
	}
	plasmaWatchedRecipients.Store(&plasmaRecipientSnapshot{addrs: m})
	return len(m)
}

func isWatchedPlasmaRecipient(to common.Address) bool {
	snap := plasmaWatchedRecipients.Load()
	if snap == nil || len(snap.addrs) == 0 {
		return false
	}
	_, ok := snap.addrs[strings.ToLower(to.Hex())]
	return ok
}
