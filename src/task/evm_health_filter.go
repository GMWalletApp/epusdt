package task

import (
	"math/big"
	"sort"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
)

var transferEventHash = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")

func evmBackfillBatchSize(network string) int64 {
	if network == mdb.NetworkBsc {
		return 200
	}
	return 1000
}
func loadChainTokenContracts(network, logPrefix string) []common.Address {
	tokens, err := data.ListEnabledChainTokensByNetwork(network)
	if err != nil {
		log.Sugar.Errorf("%s load chain_tokens err=%v", logPrefix, err)
		return nil
	}
	addrs := make([]common.Address, 0, len(tokens))
	for _, t := range tokens {
		c := strings.TrimSpace(t.ContractAddress)
		if c == "" {
			continue
		}
		addrs = append(addrs, common.HexToAddress(c))
	}
	return addrs
}

func loadEvmRecipientTopics(network, logPrefix string) []common.Hash {
	wallets, err := data.GetAvailableWalletAddressByNetwork(network)
	if err != nil {
		if logPrefix != "" {
			log.Sugar.Warnf("%s load wallet addresses: %v", logPrefix, err)
		}
		return nil
	}
	return evmRecipientTopicsFromWallets(wallets)
}

func evmRecipientTopicsFromWallets(wallets []mdb.WalletAddress) []common.Hash {
	seen := make(map[string]struct{}, len(wallets))
	topics := make([]common.Hash, 0, len(wallets))
	for _, w := range wallets {
		a := strings.TrimSpace(w.Address)
		if !common.IsHexAddress(a) {
			continue
		}
		addr := common.HexToAddress(a)
		key := strings.ToLower(addr.Hex())
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		topics = append(topics, common.BytesToHash(addr.Bytes()))
	}
	sort.Slice(topics, func(i, j int) bool {
		return strings.ToLower(topics[i].Hex()) < strings.ToLower(topics[j].Hex())
	})
	return topics
}

func evmTransferTopics(recipientTopics []common.Hash) [][]common.Hash {
	if len(recipientTopics) == 0 {
		return [][]common.Hash{{transferEventHash}}
	}
	return [][]common.Hash{{transferEventHash}, nil, recipientTopics}
}

// evmLiveFilterQuery starts a WebSocket subscription at the current head.
// go-ethereum otherwise serializes a nil FromBlock as 0x0, which makes public
// RPC providers reject the subscription as an oversized historical range.
func evmLiveFilterQuery(contracts []common.Address, recipientTopics []common.Hash) ethereum.FilterQuery {
	return ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(rpc.LatestBlockNumber)),
		Addresses: contracts,
		Topics:    evmTransferTopics(recipientTopics),
	}
}
