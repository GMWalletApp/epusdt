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
	// Polygon PoS 主网 USDT / USDC（原生）/ USDC.e（桥接）
	polygonUsdtContract  = common.HexToAddress("0xc2132D05D31c914a87C6611C10748AEb04B58e8F")
	polygonUsdcContract  = common.HexToAddress("0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359")
	polygonUsdcEContract = common.HexToAddress("0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174")
)

type polygonRecipientSnapshot struct {
	addrs map[string]struct{}
}

var polygonWatchedRecipients atomic.Pointer[polygonRecipientSnapshot]

func StartPolygonWebSocketListener() {
	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkPolygon)
	if err != nil {
		log.Sugar.Errorf("[POLYGON-WS] Failed to get wallet addresses: %v", err)
		return
	}
	storePolygonRecipientsFromWallets(wallets)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			w, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkPolygon)
			if err != nil {
				log.Sugar.Warnf("[POLYGON-WS] refresh wallet addresses: %v", err)
				continue
			}
			storePolygonRecipientsFromWallets(w)
		}
	}()
	wsURL := "wss://polygon-bor-rpc.publicnode.com"
	query := ethereum.FilterQuery{
		Addresses: []common.Address{
			polygonUsdtContract,
			polygonUsdcContract,
			polygonUsdcEContract,
		},
		Topics: [][]common.Hash{},
	}

	runEvmWsLogListener("[POLYGON-WS]", wsURL, query, evmBackfillLookbackBlocks(2*time.Second), func(client *ethclient.Client, vLog types.Log) {
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
			log.Sugar.Warnf("[POLYGON-WS] HeaderByNumber block=%d: %v, using local time", vLog.BlockNumber, err)
			blockTsMs = time.Now().UnixMilli()
		} else {
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
