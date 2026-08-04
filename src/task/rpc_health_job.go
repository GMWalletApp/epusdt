package task

import (
	"context"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const rpcProbeTimeout = 5 * time.Second

// RpcHealthJob periodically probes enabled general/both rpc_nodes rows.
// Manual verification nodes are left for on-demand admin checks so paid
// endpoints are not consumed by the scheduler.
type RpcHealthJob struct{}

var gRpcHealthJobLock sync.Mutex

func (r RpcHealthJob) Run() {
	gRpcHealthJobLock.Lock()
	defer gRpcHealthJobLock.Unlock()

	nodes, err := data.ListRpcNodesForHealth()
	if err != nil {
		log.Sugar.Errorf("[rpc-health] list nodes err=%v", err)
		return
	}
	var wg sync.WaitGroup
	for i := range nodes {
		if !nodes[i].Enabled {
			continue
		}
		wg.Add(1)
		go func(n mdb.RpcNode) {
			defer wg.Done()
			status, latency, probeErr := probeRpcNode(n)
			if status == mdb.RpcNodeStatusDown && n.Status != mdb.RpcNodeStatusDown {
				log.Sugar.Errorf("[rpc-health] capability probe failed node=%s err=%v", data.RpcNodeLogLabel(n), probeErr)
			}
			if err := data.UpdateRpcNodeHealth(n.ID, status, latency); err != nil {
				log.Sugar.Warnf("[rpc-health] update node %d err=%v", n.ID, err)
			}
		}(nodes[i])
	}
	wg.Wait()
}

// ProbeRpcNode verifies the capability required by the configured node. EVM
// HTTP nodes must serve both block height and the exact historical log filter
// used by the backfill scanner; EVM WS nodes must accept that log subscription.
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

func isEvmRpcNetwork(network string) bool {
	switch network {
	case mdb.NetworkEthereum, mdb.NetworkBsc, mdb.NetworkPolygon, mdb.NetworkPlasma:
		return true
	default:
		return false
	}
}

func probeEvmRpcNode(ctx context.Context, node mdb.RpcNode) error {
	client, err := ethclient.DialContext(ctx, strings.TrimSpace(node.Url))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

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
