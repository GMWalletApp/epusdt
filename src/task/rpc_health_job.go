package task

import (
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"
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
			status, latency := ProbeNode(n.Url)
			if err := data.UpdateRpcNodeHealth(n.ID, status, latency); err != nil {
				log.Sugar.Warnf("[rpc-health] update node %d err=%v", n.ID, err)
			}
		}(nodes[i])
	}
	wg.Wait()
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
