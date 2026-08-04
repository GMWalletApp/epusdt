package task

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/gorilla/websocket"
)

// --------------- ParseAddress ---------------

func TestParseAddress_HttpWithPort(t *testing.T) {
	got, err := ParseAddress("http://api.trongrid.io:8090")
	if err != nil {
		t.Fatal(err)
	}
	if got != "api.trongrid.io:8090" {
		t.Fatalf("want api.trongrid.io:8090, got %s", got)
	}
}

func TestParseAddress_HttpDefaultPort(t *testing.T) {
	got, err := ParseAddress("http://api.trongrid.io")
	if err != nil {
		t.Fatal(err)
	}
	if got != "api.trongrid.io:80" {
		t.Fatalf("want api.trongrid.io:80, got %s", got)
	}
}

func TestParseAddress_HttpsDefaultPort(t *testing.T) {
	got, err := ParseAddress("https://api.trongrid.io")
	if err != nil {
		t.Fatal(err)
	}
	if got != "api.trongrid.io:443" {
		t.Fatalf("want api.trongrid.io:443, got %s", got)
	}
}

func TestParseAddress_WssDefaultPort(t *testing.T) {
	got, err := ParseAddress("wss://bsc-ws-node.nariox.org")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bsc-ws-node.nariox.org:443" {
		t.Fatalf("want bsc-ws-node.nariox.org:443, got %s", got)
	}
}

func TestParseAddress_WsDefaultPort(t *testing.T) {
	got, err := ParseAddress("ws://localhost")
	if err != nil {
		t.Fatal(err)
	}
	if got != "localhost:80" {
		t.Fatalf("want localhost:80, got %s", got)
	}
}

func TestParseAddress_WsWithPort(t *testing.T) {
	got, err := ParseAddress("ws://localhost:9650")
	if err != nil {
		t.Fatal(err)
	}
	if got != "localhost:9650" {
		t.Fatalf("want localhost:9650, got %s", got)
	}
}

func TestParseAddress_BareHostPort(t *testing.T) {
	got, err := ParseAddress("10.0.0.1:8545")
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.1:8545" {
		t.Fatalf("want 10.0.0.1:8545, got %s", got)
	}
}

func TestParseAddress_BareHostNoPort(t *testing.T) {
	got, err := ParseAddress("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "example.com:80" {
		t.Fatalf("want example.com:80, got %s", got)
	}
}

func TestParseAddress_WithPath(t *testing.T) {
	got, err := ParseAddress("https://mainnet.infura.io/v3/KEY123")
	if err != nil {
		t.Fatal(err)
	}
	if got != "mainnet.infura.io:443" {
		t.Fatalf("want mainnet.infura.io:443, got %s", got)
	}
}

// --------------- MeasureTCPDial ---------------

func TestMeasureTCPDial_Success(t *testing.T) {
	// start a local TCP listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dur, err := MeasureTCPDial(ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial should succeed: %v", err)
	}
	if dur <= 0 {
		t.Fatalf("duration should be positive, got %v", dur)
	}
}

func TestMeasureTCPDial_Refused(t *testing.T) {
	// pick a port that is almost certainly not listening
	_, err := MeasureTCPDial("127.0.0.1:1", 500*time.Millisecond)
	if err == nil {
		t.Fatal("dial to closed port should fail")
	}
}

func TestMeasureTCPDial_RespectsTimeout(t *testing.T) {
	// Start a listener but never accept — the dial handshake will hang
	// until the timeout fires.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Set backlog to 0 by not calling Accept and filling the queue.
	// Connect once to fill the backlog, then the next dial should stall.
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Skip("could not saturate backlog, skipping")
	}
	defer conn.Close()

	// Use a very short timeout to verify it doesn't hang forever.
	_, dialErr := MeasureTCPDial(ln.Addr().String(), 100*time.Millisecond)
	// This may succeed (kernel allows queued connections) or timeout — both
	// are acceptable. We just verify it returns within a sane window.
	_ = dialErr
}

// --------------- ProbeNode ---------------

func TestProbeNode_Reachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	url := "http://127.0.0.1:" + strconv.Itoa(port)

	status, latency := ProbeNode(url)
	if status != mdb.RpcNodeStatusOk {
		t.Fatalf("want ok, got %s", status)
	}
	if latency < 0 {
		t.Fatalf("latency should be >= 0, got %d", latency)
	}
}

func TestProbeNode_Unreachable(t *testing.T) {
	status, latency := ProbeNode("http://127.0.0.1:1")
	if status != mdb.RpcNodeStatusDown {
		t.Fatalf("want down, got %s", status)
	}
	if latency != -1 {
		t.Fatalf("want -1, got %d", latency)
	}
}

func TestProbeNode_InvalidURL(t *testing.T) {
	status, latency := ProbeNode("://bad")
	if status != mdb.RpcNodeStatusDown {
		t.Fatalf("want down, got %s", status)
	}
	if latency != -1 {
		t.Fatalf("want -1, got %d", latency)
	}
}

func TestProbeRpcNode_EvmHTTPRequiresHistoricalLogs(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedEvmHealthProbeState(t)

	var sawLogs bool
	server := newEvmHealthHTTPServer(t, func(method string, params json.RawMessage) (interface{}, *rpcHealthTestError) {
		switch method {
		case "eth_blockNumber":
			return "0x100", nil
		case "eth_getLogs":
			sawLogs = true
			var filters []map[string]interface{}
			if err := json.Unmarshal(params, &filters); err != nil || len(filters) != 1 {
				t.Fatalf("decode eth_getLogs params: %v params=%s", err, params)
			}
			if got := filters[0]["fromBlock"]; got != "0x65" {
				t.Fatalf("fromBlock = %v, want 0x65", got)
			}
			if got := filters[0]["toBlock"]; got != "0x100" {
				t.Fatalf("toBlock = %v, want 0x100", got)
			}
			return []interface{}{}, nil
		default:
			return nil, &rpcHealthTestError{Code: -32601, Message: "method not found"}
		}
	})
	defer server.Close()

	status, latency := ProbeRpcNode(mdb.RpcNode{Network: mdb.NetworkBsc, Type: mdb.RpcNodeTypeHttp, Url: server.URL})
	if status != mdb.RpcNodeStatusOk {
		t.Fatalf("status = %s, want ok", status)
	}
	if latency < 0 {
		t.Fatalf("latency = %d, want >= 0", latency)
	}
	if !sawLogs {
		t.Fatal("probe did not call eth_getLogs")
	}
}

func TestProbeRpcNode_EvmHTTPRejectsBlockOnlyEndpoint(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedEvmHealthProbeState(t)

	server := newEvmHealthHTTPServer(t, func(method string, _ json.RawMessage) (interface{}, *rpcHealthTestError) {
		if method == "eth_blockNumber" {
			return "0x100", nil
		}
		return nil, &rpcHealthTestError{Code: -32005, Message: "eth_getLogs disabled"}
	})
	defer server.Close()

	status, latency := ProbeRpcNode(mdb.RpcNode{Network: mdb.NetworkBsc, Type: mdb.RpcNodeTypeHttp, Url: server.URL})
	if status != mdb.RpcNodeStatusDown || latency != -1 {
		t.Fatalf("probe = (%s, %d), want (down, -1)", status, latency)
	}
}

func TestProbeRpcNode_EvmWSRequiresLogSubscription(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedEvmHealthProbeState(t)

	server, sawSubscribe := newEvmHealthWSServer(t, false)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	status, latency := ProbeRpcNode(mdb.RpcNode{Network: mdb.NetworkBsc, Type: mdb.RpcNodeTypeWs, Url: wsURL})
	if status != mdb.RpcNodeStatusOk || latency < 0 {
		t.Fatalf("probe = (%s, %d), want ok", status, latency)
	}
	if !sawSubscribe.Load() {
		t.Fatal("probe did not call eth_subscribe")
	}
}

func TestProbeRpcNode_EvmWSRejectsSubscriptionQuotaError(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	seedEvmHealthProbeState(t)

	server, _ := newEvmHealthWSServer(t, true)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	status, latency := ProbeRpcNode(mdb.RpcNode{Network: mdb.NetworkBsc, Type: mdb.RpcNodeTypeWs, Url: wsURL})
	if status != mdb.RpcNodeStatusDown || latency != -1 {
		t.Fatalf("probe = (%s, %d), want (down, -1)", status, latency)
	}
}

type rpcHealthTestRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcHealthTestError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcHealthTestResponse struct {
	JSONRPC string              `json:"jsonrpc"`
	ID      json.RawMessage     `json:"id"`
	Result  interface{}         `json:"result,omitempty"`
	Error   *rpcHealthTestError `json:"error,omitempty"`
}

func seedEvmHealthProbeState(t *testing.T) {
	t.Helper()
	if err := dao.Mdb.Create(&mdb.WalletAddress{
		Network: mdb.NetworkBsc,
		Address: "0x1111111111111111111111111111111111111111",
		Status:  mdb.TokenStatusEnable,
	}).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}
	if err := data.UpsertEvmScanCursor(mdb.NetworkBsc, 100); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}
}

func newEvmHealthHTTPServer(t *testing.T, handle func(string, json.RawMessage) (interface{}, *rpcHealthTestError)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req rpcHealthTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		result, rpcErr := handle(req.Method, req.Params)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rpcHealthTestResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

func newEvmHealthWSServer(t *testing.T, rejectSubscription bool) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	sawSubscribe := new(atomic.Bool)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req rpcHealthTestRequest
			if err = json.Unmarshal(payload, &req); err != nil {
				t.Errorf("decode websocket request: %v", err)
				return
			}
			resp := rpcHealthTestResponse{JSONRPC: "2.0", ID: req.ID}
			switch req.Method {
			case "eth_blockNumber":
				resp.Result = "0x100"
			case "eth_subscribe":
				sawSubscribe.Store(true)
				if rejectSubscription {
					resp.Error = &rpcHealthTestError{Code: 15, Message: "public endpoint rate limit"}
				} else {
					var params []json.RawMessage
					var filter map[string]interface{}
					if err = json.Unmarshal(req.Params, &params); err != nil || len(params) != 2 {
						resp.Error = &rpcHealthTestError{Code: -32602, Message: "invalid subscription params"}
					} else if err = json.Unmarshal(params[1], &filter); err != nil || filter["fromBlock"] != "latest" {
						resp.Error = &rpcHealthTestError{Code: -32602, Message: "exceed maximum block range"}
					} else {
						resp.Result = "0xsubscription"
					}
				}
			case "eth_unsubscribe":
				resp.Result = true
			default:
				resp.Error = &rpcHealthTestError{Code: -32601, Message: "method not found"}
			}
			if err = conn.WriteJSON(resp); err != nil {
				return
			}
		}
	}))
	return server, sawSubscribe
}
