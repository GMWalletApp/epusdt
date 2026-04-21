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
	// USDT / USDC 合约地址（BSC 主网）
	bscUsdtContract = common.HexToAddress("0x55d398326f99059fF775485246999027B3197955")
	bscUsdcContract = common.HexToAddress("0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d")
)

type bscRecipientSnapshot struct {
	addrs map[string]struct{}
}

var bscWatchedRecipients atomic.Pointer[bscRecipientSnapshot]

func StartBscWebSocketListener() {
	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkBsc)
	if err != nil {
		log.Sugar.Errorf("[BSC-WS] Failed to get wallet addresses: %v", err)
		return
	}
	storeBscRecipientsFromWallets(wallets)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			w, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkBsc)
			if err != nil {
				log.Sugar.Warnf("[BSC-WS] refresh wallet addresses: %v", err)
				continue
			}
			storeBscRecipientsFromWallets(w)
		}
	}()
	wsURL := "wss://bsc.drpc.org"
	query := ethereum.FilterQuery{
		Addresses: []common.Address{
			bscUsdtContract,
			bscUsdcContract,
		},
		Topics: [][]common.Hash{},
	}

	runEvmWsLogListener("[BSC-WS]", wsURL, query, evmBackfillLookbackBlocks(3*time.Second), func(client *ethclient.Client, vLog types.Log) {
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
			log.Sugar.Warnf("[BSC-WS] HeaderByNumber block=%d: %v, using local time", vLog.BlockNumber, err)
			blockTsMs = time.Now().UnixMilli()
		} else {
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
