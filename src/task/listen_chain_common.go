package task

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"

	"github.com/ethereum/go-ethereum/common"
)

// chainEnabledWatchdog returns a cancellable context whose cancel() is
// invoked when either:
//  1. IsChainEnabled(network) returns false — admin disabled the chain
//  2. The enabled-token fingerprint changes — admin added/removed/
//     toggled a chain_tokens row for this network
//
// Both cases need the listener to exit so the outer loop can reconnect
// with the fresh token set (EVM WebSocket subscriptions are fixed at
// connect time; to pick up a new contract we must re-subscribe).
//
// initialFingerprint is the fingerprint computed BEFORE connecting; the
// watchdog compares every 10s tick against this baseline. Caller must
// defer the returned cancel func to release the goroutine.
func chainEnabledWatchdog(network, logPrefix, initialFingerprint string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !data.IsChainEnabled(network) {
					log.Sugar.Infof("%s chain disabled, stopping listener", logPrefix)
					cancel()
					return
				}
				if fp := chainTokenFingerprint(network); fp != initialFingerprint {
					log.Sugar.Infof("%s chain_tokens changed (was %q → now %q), reconnecting", logPrefix, initialFingerprint, fp)
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

// chainTokenFingerprint returns a stable string representing the
// enabled-token set for a network. Used by chainEnabledWatchdog to
// detect admin changes between polls.
func chainTokenFingerprint(network string) string {
	tokens, err := data.ListEnabledChainTokensByNetwork(network)
	if err != nil {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		parts = append(parts, strings.ToLower(strings.TrimSpace(t.ContractAddress))+"|"+strings.ToUpper(strings.TrimSpace(t.Symbol)))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// loadChainTokenContracts reads enabled tokens for a network and returns
// their contract addresses as ethereum-go common.Address values. Rows
// with blank contract_address (e.g. Solana native SOL marker) are
// skipped. Callers use the length to decide whether to connect or idle.
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
		if !common.IsHexAddress(c) {
			log.Sugar.Warnf("%s skip invalid token contract network=%s symbol=%s contract=%q", logPrefix, network, t.Symbol, c)
			continue
		}
		addrs = append(addrs, common.HexToAddress(c))
	}
	return addrs
}

// resolveChainWsURL picks a healthy WS endpoint from rpc_nodes for the
// given network. If no enabled node is configured, the caller skips the
// current listener run so admin-side disabled/deleted rows are respected.
func resolveChainWsURL(network, logPrefix string) (string, bool) {
	node, ok := resolveChainWsNode(network, logPrefix)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(node.Url), true
}

func resolveChainWsNode(network, logPrefix string, excludeIDs ...uint64) (mdb.RpcNode, bool) {
	node, err := data.SelectGeneralRpcNode(network, mdb.RpcNodeTypeWs, excludeIDs...)
	if err == nil && node != nil && node.ID > 0 {
		rpcURL := strings.TrimSpace(node.Url)
		if rpcURL != "" {
			node.Url = rpcURL
			return *node, true
		}
		log.Sugar.Errorf("%s rpc_nodes id=%d has empty url", logPrefix, node.ID)
		return mdb.RpcNode{}, false
	}
	if err != nil {
		log.Sugar.Errorf("%s resolve rpc_nodes err=%v", logPrefix, err)
	} else {
		log.Sugar.Warnf("%s no enabled %s WS RPC node configured in rpc_nodes", logPrefix, network)
	}
	return mdb.RpcNode{}, false
}
