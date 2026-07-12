package task

import (
	"context"
	"math/big"
	"strings"
	"sync"
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

type addedEvmRecipientSnapshot struct {
	addrs map[string]struct{}
}

var addedEvmRecipients sync.Map

func StartBaseWebSocketListener() {
	startAddedEvmWebSocketListener(mdb.NetworkBase, "[BASE-WS]")
}

func StartArbitrumWebSocketListener() {
	startAddedEvmWebSocketListener(mdb.NetworkArbitrum, "[ARBITRUM-WS]")
}

func startAddedEvmWebSocketListener(network, logPrefix string) {
	for {
		if data.IsChainEnabled(network) {
			if contracts := loadChainTokenContracts(network, logPrefix); len(contracts) > 0 {
				runAddedEvmListener(network, logPrefix, contracts)
			}
		}
		time.Sleep(10 * time.Second)
	}
}

func runAddedEvmListener(network, logPrefix string, contracts []common.Address) {
	ctx, cancel := chainEnabledWatchdog(network, logPrefix, chainTokenFingerprint(network))
	defer cancel()

	wallets, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		log.Sugar.Errorf("%s failed to get wallet addresses: %v", logPrefix, err)
		return
	}
	storeAddedEvmRecipients(network, wallets)
	go refreshAddedEvmRecipients(ctx, network, logPrefix)

	wsNode, ok := resolveChainWsNode(network, logPrefix)
	if !ok {
		return
	}
	log.Sugar.Infof("%s connecting using WSS node %s watching %d contract(s)", logPrefix, data.RpcNodeLogLabel(wsNode), len(contracts))
	query := ethereum.FilterQuery{Addresses: contracts, Topics: [][]common.Hash{}}
	runEvmWsLogListener(ctx, network, logPrefix, wsNode, query, func(client *ethclient.Client, vLog types.Log) {
		processAddedEvmLog(client, network, logPrefix, vLog)
	})
}

func refreshAddedEvmRecipients(ctx context.Context, network, logPrefix string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			wallets, err := data.GetAvailableWalletAddressByNetwork(network)
			if err != nil {
				log.Sugar.Warnf("%s refresh wallet addresses: %v", logPrefix, err)
				continue
			}
			storeAddedEvmRecipients(network, wallets)
		}
	}
}

func processAddedEvmLog(client *ethclient.Client, network, logPrefix string, vLog types.Log) {
	if len(vLog.Topics) < 3 || vLog.Topics[0] != transferEventHash {
		return
	}
	toAddr := common.HexToAddress(vLog.Topics[2].Hex())
	if !isWatchedAddedEvmRecipient(network, toAddr) {
		return
	}
	amount := new(big.Int).SetBytes(vLog.Data)
	blockTsMs := time.Now().UnixMilli()
	header, err := client.HeaderByNumber(context.Background(), new(big.Int).SetUint64(vLog.BlockNumber))
	if err != nil {
		data.RecordRpcFailure(network)
		log.Sugar.Warnf("%s HeaderByNumber block=%d: %v, using local time", logPrefix, vLog.BlockNumber, err)
	} else {
		data.RecordRpcSuccess(network)
		blockTsMs = int64(header.Time) * 1000
	}
	service.TryProcessEvmERC20Transfer(network, vLog.Address, toAddr, amount, vLog.TxHash.Hex(), blockTsMs)
}

func storeAddedEvmRecipients(network string, wallets []mdb.WalletAddress) int {
	addrs := make(map[string]struct{})
	for _, wallet := range wallets {
		address := strings.TrimSpace(wallet.Address)
		if common.IsHexAddress(address) {
			addrs[strings.ToLower(common.HexToAddress(address).Hex())] = struct{}{}
		}
	}
	addedEvmRecipients.Store(network, &addedEvmRecipientSnapshot{addrs: addrs})
	return len(addrs)
}

func isWatchedAddedEvmRecipient(network string, address common.Address) bool {
	value, ok := addedEvmRecipients.Load(network)
	if !ok {
		return false
	}
	snapshot, ok := value.(*addedEvmRecipientSnapshot)
	if !ok || snapshot == nil {
		return false
	}
	_, ok = snapshot.addrs[strings.ToLower(address.Hex())]
	return ok
}
