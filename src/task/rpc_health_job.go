package task

import (
	"context"
	"fmt"
	scanner "github.com/GMWalletApp/ChainScanner"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const rpcProbeTimeout = 5 * time.Second

func scannerNodeConfig(node mdb.RpcNode, candidates []mdb.RpcNode) (scanner.Network, bool) {
	n := scanner.Network{Name: node.Network, Protocol: node.Type, URL: node.Url, APIKey: node.ApiKey}
	if scanner.NeedsHistory(n.Name, n.Protocol) {
		for _, other := range candidates {
			if other.ID == node.ID || other.Network != node.Network {
				continue
			}
			if other.Type == mdb.RpcNodeTypeHttp || other.Type == "rest" || (n.Name == "solana" && other.Type == "jsonrpc") {
				n.HistoryURL = other.Url
				n.HistoryAPIKey = other.ApiKey
				break
			}
		}
	}
	return n, scanner.ValidateNetwork(n) == nil
}

// ProbeRpcNode verifies the capability required by the configured node. EVM
// scanning nodes must serve the historical log filter or subscription
// used by the scanner. Manual-only EVM nodes are checked by reading a block header.
// Other node types retain the legacy TCP reachability probe.
func ProbeRpcNode(node mdb.RpcNode) (string, int) {
	status, latency, _ := probeRpcNode(node)
	return status, latency
}

func probeRpcNode(node mdb.RpcNode) (string, int, error) {
	node.Network = strings.ToLower(strings.TrimSpace(node.Network))
	node.Type = strings.ToLower(strings.TrimSpace(node.Type))
	if isEvmRpcNetwork(node.Network) && (node.Type == mdb.RpcNodeTypeHttp || node.Type == mdb.RpcNodeTypeWs) {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), rpcProbeTimeout)
		defer cancel()
		if err := probeEvmRpcNode(ctx, node); err != nil {
			return mdb.RpcNodeStatusDown, -1, err
		}
		return mdb.RpcNodeStatusOk, int(time.Since(start).Milliseconds()), nil
	}

	if node.Type != "http" && node.Type != "lite" || node.Network == "sui" {
		start := time.Now()
		var candidates []mdb.RpcNode
		if scanner.NeedsHistory(node.Network, node.Type) {
			var err error
			candidates, err = data.GeneralScannerNodes(node.Network, scanner.Protocols(node.Network))
			if err != nil {
				return mdb.RpcNodeStatusDown, -1, err
			}
		}
		n, ok := scannerNodeConfig(node, candidates)
		if !ok {
			return mdb.RpcNodeStatusDown, -1, fmt.Errorf("invalid or missing scanner endpoint")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		src, err := scanner.NewSource(ctx, n)
		if err != nil {
			return mdb.RpcNodeStatusDown, -1, fmt.Errorf("%s %s node open failed (%T)", node.Network, node.Type, err)
		}
		defer src.Close()
		if _, err = src.Head(ctx); err != nil {
			return mdb.RpcNodeStatusDown, -1, fmt.Errorf("%s %s head check failed (%T)", node.Network, node.Type, err)
		}
		return mdb.RpcNodeStatusOk, int(time.Since(start).Milliseconds()), nil
	}
	addr, err := ParseAddress(node.Url)
	if err != nil {
		return mdb.RpcNodeStatusDown, -1, err
	}
	dur, err := MeasureTCPDial(addr, rpcProbeTimeout)
	if err != nil {
		return mdb.RpcNodeStatusDown, -1, err
	}
	return mdb.RpcNodeStatusOk, int(dur.Milliseconds()), nil
}

func isEvmRpcNetwork(network string) bool { return scanner.IsEVM(network) }

func verifyProbeChain(ctx context.Context, client *ethclient.Client, name string) error {
	id, err := client.ChainID(ctx)
	if err != nil {
		return err
	}
	if id.Cmp(big.NewInt(scanner.EVMChainID(name))) != 0 {
		return fmt.Errorf("node chain ID %s differs from %s", id, name)
	}
	return nil
}
func probeEvmRpcNode(ctx context.Context, node mdb.RpcNode) error {
	client, err := ethclient.DialContext(ctx, strings.TrimSpace(node.Url))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

	if err = verifyProbeChain(ctx, client, node.Network); err != nil {
		return err
	}
	// Manual verification reads receipts and block headers and does not scan logs.
	if data.NormalizeRpcNodePurpose(node.Purpose) == mdb.RpcNodePurposeManualVerify {
		_, err := client.HeaderByNumber(ctx, nil)
		return err
	}

	head, err := client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("block number: %w", err)
	}
	query, hasFilter, err := evmHealthFilterQuery(node.Network, node.Type, head)
	if err != nil {
		return err
	}
	if !hasFilter {
		return nil
	}

	if node.Type == mdb.RpcNodeTypeWs {
		logsCh := make(chan types.Log)
		sub, err := client.SubscribeFilterLogs(ctx, query, logsCh)
		if err != nil {
			return fmt.Errorf("subscribe logs: %w", err)
		}
		sub.Unsubscribe()
		return nil
	}

	if _, err := client.FilterLogs(ctx, query); err != nil {
		return fmt.Errorf("filter logs: %w", err)
	}
	return nil
}

func evmHealthFilterQuery(network, nodeType string, head uint64) (ethereum.FilterQuery, bool, error) {
	contracts := loadChainTokenContracts(network, "")
	recipients := loadEvmRecipientTopics(network, "")
	if len(contracts) == 0 || len(recipients) == 0 {
		return ethereum.FilterQuery{}, false, nil
	}

	query := ethereum.FilterQuery{
		Addresses: contracts,
		Topics:    evmTransferTopics(recipients),
	}
	if nodeType != mdb.RpcNodeTypeHttp {
		return evmLiveFilterQuery(contracts, recipients), true, nil
	}
	if head > uint64(1<<63-1) {
		return ethereum.FilterQuery{}, false, fmt.Errorf("block number exceeds int64 range")
	}

	headBlock := int64(head)
	fromBlock := headBlock
	cursor, err := data.GetEvmScanCursor(network)
	if err != nil {
		return ethereum.FilterQuery{}, false, fmt.Errorf("load scan cursor: %w", err)
	}
	if cursor.ID > 0 && cursor.LastBlock >= 0 && cursor.LastBlock < headBlock {
		fromBlock = cursor.LastBlock + 1
	}
	toBlock := fromBlock + evmBackfillBatchSize(network) - 1
	if toBlock > headBlock {
		toBlock = headBlock
	}
	query.FromBlock = big.NewInt(fromBlock)
	query.ToBlock = big.NewInt(toBlock)
	return query, true, nil
}

// ProbeNode does a TCP dial to the RPC URL and returns (status, latencyMs).
// Exported so the admin controller can reuse it without duplicating logic.
func ProbeNode(rawURL string) (string, int) {
	addr, err := ParseAddress(rawURL)
	if err != nil {
		return mdb.RpcNodeStatusDown, -1
	}
	dur, err := MeasureTCPDial(addr, rpcProbeTimeout)
	if err != nil {
		return mdb.RpcNodeStatusDown, -1
	}
	return mdb.RpcNodeStatusOk, int(dur.Milliseconds())
}

func ParseAddress(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "tcp://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https", "wss":
			port = "443"
		default:
			port = "80"
		}
	}

	return host + ":" + port, nil
}

func MeasureTCPDial(addr string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	return time.Since(start), nil
}
