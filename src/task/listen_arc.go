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

// This is the testnet for Arc USDC/EURC network.
const ArcEURCAddress = "0x89B50855Aa3bE2F677cD6303Cec089B5F319D72a"
const ArcEURCDecimals = 6
const ArcUSDCERC20Address = "0x3600000000000000000000000000000000000000"
const ArcUSDCSystemAddress = "0x1800000000000000000000000000000000000000"
const ArcNativeUSDCDecimals = 18
const ArcWs = "wss://rpc.testnet.arc.network"
const ArcChainID = "5042002"

type arcRecipientSnapshot struct {
	addrs map[string]struct{}
}

var arcWatchedRecipients atomic.Pointer[arcRecipientSnapshot]

var arcERC20Tokens = []mdb.ChainToken{
	{Network: mdb.NetworkArc, Symbol: "EURC", ContractAddress: ArcEURCAddress, Decimals: ArcEURCDecimals, Enabled: true},
}

var arcNativeUSDCTransferEventHash = common.HexToHash("0x62f084c00a442dcf51cdbb51beed2839bf42a268da8474b0e98f38edb7db5a22")

func StartArcWebSocketListener() {
	for {
		runArcListener()
		time.Sleep(10 * time.Second)
	}
}

func runArcListener() {
	ctx := context.Background()

	wallets, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkArc)
	if err != nil {
		log.Sugar.Errorf("[ARC-WS] Failed to get wallet addresses: %v", err)
		return
	}
	storeArcRecipientsFromWallets(wallets)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w, err := data.GetAvailableWalletAddressByNetwork(mdb.NetworkArc)
				if err != nil {
					log.Sugar.Warnf("[ARC-WS] refresh wallet addresses: %v", err)
					continue
				}
				storeArcRecipientsFromWallets(w)
			}
		}
	}()

	contracts := make([]common.Address, 0, len(arcERC20Tokens)+1)
	tokenByContract := make(map[string]mdb.ChainToken, len(arcERC20Tokens)+1)
	for _, token := range arcERC20Tokens {
		contract := common.HexToAddress(token.ContractAddress)
		contracts = append(contracts, contract)
		tokenByContract[strings.ToLower(contract.Hex())] = token
	}

	// Arc USDC is a native gas token. Its single source of truth for balance
	// movement logs is the native-token system contract, not the ERC-20 facade.
	usdcSystemContract := common.HexToAddress(ArcUSDCSystemAddress)
	contracts = append(contracts, usdcSystemContract)
	tokenByContract[strings.ToLower(usdcSystemContract.Hex())] = mdb.ChainToken{
		Network:         mdb.NetworkArc,
		Symbol:          "USDC",
		ContractAddress: ArcUSDCERC20Address,
		Decimals:        ArcNativeUSDCDecimals,
		Enabled:         true,
	}

	log.Sugar.Infof("[ARC-WS] connecting to %s chain_id=%s watching %d contract(s)", ArcWs, ArcChainID, len(contracts))

	query := ethereum.FilterQuery{
		Addresses: contracts,
		Topics:    [][]common.Hash{{transferEventHash, arcNativeUSDCTransferEventHash}},
	}

	runEvmWsLogListener(ctx, "[ARC-WS]", ArcWs, query, func(client *ethclient.Client, vLog types.Log) {
		handleArcTransferLog(client, tokenByContract, "[ARC-WS]", vLog)
	})
}

func handleArcTransferLog(client *ethclient.Client, tokenByContract map[string]mdb.ChainToken, logPrefix string, vLog types.Log) {
	if vLog.Removed {
		return
	}
	if len(vLog.Topics) < 3 {
		return
	}
	if !isArcTransferEvent(vLog) {
		return
	}

	toAddr := common.HexToAddress(vLog.Topics[2].Hex())
	if !isWatchedArcRecipient(toAddr) {
		return
	}

	token, ok := tokenByContract[strings.ToLower(vLog.Address.Hex())]
	if !ok {
		return
	}

	amount := new(big.Int).SetBytes(vLog.Data)
	log.Sugar.Infof("%s matched %s transfer contract=%s to=%s raw_amount=%s tx_hash=%s block=%d", logPrefix, token.Symbol, vLog.Address.Hex(), toAddr.Hex(), amount.String(), vLog.TxHash.Hex(), vLog.BlockNumber)

	var blockTsMs int64
	header, err := client.HeaderByNumber(context.Background(), new(big.Int).SetUint64(vLog.BlockNumber))
	if err != nil {
		log.Sugar.Warnf("%s HeaderByNumber block=%d: %v, using local time", logPrefix, vLog.BlockNumber, err)
		blockTsMs = time.Now().UnixMilli()
	} else {
		blockTsMs = int64(header.Time) * 1000
	}

	service.TryProcessConfiguredEvmERC20Transfer(mdb.NetworkArc, token, toAddr, amount, vLog.TxHash.Hex(), blockTsMs)
}

func isArcTransferEvent(vLog types.Log) bool {
	if len(vLog.Topics) == 0 {
		return false
	}
	if strings.EqualFold(vLog.Address.Hex(), ArcUSDCSystemAddress) {
		return vLog.Topics[0] == arcNativeUSDCTransferEventHash
	}
	if vLog.Topics[0] == transferEventHash {
		return true
	}
	return false
}

func storeArcRecipientsFromWallets(wallets []mdb.WalletAddress) int {
	m := make(map[string]struct{})
	for _, w := range wallets {
		a := strings.TrimSpace(w.Address)
		if !common.IsHexAddress(a) {
			continue
		}
		m[strings.ToLower(common.HexToAddress(a).Hex())] = struct{}{}
	}
	arcWatchedRecipients.Store(&arcRecipientSnapshot{addrs: m})
	return len(m)
}

func isWatchedArcRecipient(to common.Address) bool {
	snap := arcWatchedRecipients.Load()
	if snap == nil || len(snap.addrs) == 0 {
		return false
	}
	_, ok := snap.addrs[strings.ToLower(to.Hex())]
	return ok
}
