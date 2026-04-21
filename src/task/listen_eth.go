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

	// USDT / USDC 合约地址（ETH 主网）
	usdtContract = common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")
	usdcContract = common.HexToAddress("0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48")

	// Transfer 事件签名
	transferEventHash = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")
)

type ethRecipientSnapshot struct {
	addrs map[string]struct{}
}

var ethWatchedRecipients atomic.Pointer[ethRecipientSnapshot]

func StartEthereumWebSocketListener() {
	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkEthereum)
	if err != nil {
		log.Sugar.Errorf("Failed to get wallet addresses: %v", err)
		return
	}
	StoreEthRecipientsFromWallets(wallets)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			w, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkEthereum)
			if err != nil {
				log.Sugar.Warnf("[ETH-WS] refresh wallet addresses: %v", err)
				continue
			}
			StoreEthRecipientsFromWallets(w)
		}
	}()
	wsURL := "wss://ethereum.publicnode.com"
	query := ethereum.FilterQuery{
		Addresses: []common.Address{
			usdtContract,
			usdcContract,
		},
		Topics: [][]common.Hash{},
	}

	runEvmWsLogListener("[ETH-WS]", wsURL, query, evmBackfillLookbackBlocks(12*time.Second), func(client *ethclient.Client, vLog types.Log) {
		if len(vLog.Topics) < 3 {
			return
		}

		event := vLog.Topics[0].String()
		if event != transferEventHash.String() {
			return
		}

		amount := new(big.Int).SetBytes(vLog.Data)

		toAddr := common.HexToAddress(vLog.Topics[2].Hex())

		if !isWatchedEthRecipient(toAddr) {
			return
		}

		var blockTsMs int64
		header, err := client.HeaderByNumber(context.Background(), big.NewInt(int64(vLog.BlockNumber)))
		if err != nil {
			log.Sugar.Warnf("[ETH-WS] HeaderByNumber block=%d: %v, using local time", vLog.BlockNumber, err)
			blockTsMs = time.Now().UnixMilli()
		} else {
			blockTsMs = int64(header.Time) * 1000
		}

		service.TryProcessEvmERC20Transfer(mdb.NetworkEthereum, vLog.Address, toAddr, amount, vLog.TxHash.Hex(), blockTsMs)
	})
}

func StoreEthRecipientsFromWallets(wallets []mdb.WalletAddress) int {
	m := make(map[string]struct{})
	for _, w := range wallets {
		a := strings.TrimSpace(w.Address)
		if !common.IsHexAddress(a) {
			continue
		}
		m[strings.ToLower(common.HexToAddress(a).Hex())] = struct{}{}
	}
	ethWatchedRecipients.Store(&ethRecipientSnapshot{addrs: m})
	return len(m)
}

func isWatchedEthRecipient(to common.Address) bool {
	snap := ethWatchedRecipients.Load()
	if snap == nil || len(snap.addrs) == 0 {
		return false
	}
	_, ok := snap.addrs[strings.ToLower(to.Hex())]
	return ok
}

func formatAmount(amount *big.Int, decimals int) string {
	f := new(big.Float).SetInt(amount)
	divisor := new(big.Float).SetFloat64(float64Pow(10, decimals))
	result := new(big.Float).Quo(f, divisor)

	return result.Text('f', 6)
}

func float64Pow(a, b int) float64 {
	result := 1.0
	for i := 0; i < b; i++ {
		result *= float64(a)
	}
	return result
}
