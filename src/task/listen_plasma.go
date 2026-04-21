package task

import (
	"context"
	"math/big"
	"strings"
	"sync/atomic"
	"time"

	"github.com/assimon/luuu/model/data"
	"github.com/assimon/luuu/model/mdb"
	"github.com/assimon/luuu/model/service"
	"github.com/assimon/luuu/util/log"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	plasmaUsdt0Contract = common.HexToAddress("0xB8CE59FC3717ada4C02eaDF9682A9e934F625ebb")
)

type plasmaRecipientSnapshot struct {
	addrs map[string]struct{}
}

var plasmaWatchedRecipients atomic.Pointer[plasmaRecipientSnapshot]

func StartPlasmaWebSocketListener() {
	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkPlasma)
	if err != nil {
		log.Sugar.Errorf("[PLASMA-WS] Failed to get wallet addresses: %v", err)
		return
	}
	storePlasmaRecipientsFromWallets(wallets)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			w, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkPlasma)
			if err != nil {
				log.Sugar.Warnf("[PLASMA-WS] refresh wallet addresses: %v", err)
				continue
			}
			storePlasmaRecipientsFromWallets(w)
		}
	}()
	// 文档提供 https://rpc.plasma.to；常见实现同主机 wss
	wsURL := "wss://rpc.plasma.to"
	query := ethereum.FilterQuery{
		Addresses: []common.Address{plasmaUsdt0Contract},
		Topics:    [][]common.Hash{},
	}

	runEvmWsLogListener("[PLASMA-WS]", wsURL, query, evmBackfillLookbackBlocks(2*time.Second), func(client *ethclient.Client, vLog types.Log) {
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
			log.Sugar.Warnf("[PLASMA-WS] HeaderByNumber block=%d: %v, using local time", vLog.BlockNumber, err)
			blockTsMs = time.Now().UnixMilli()
		} else {
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
