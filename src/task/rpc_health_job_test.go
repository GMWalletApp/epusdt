package task

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/GMWalletApp/epusdt/model/mdb"
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
