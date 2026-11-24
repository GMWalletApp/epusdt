package chains

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/testutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/core"
	geyser "github.com/rpcpool/yellowstone-grpc/examples/golang/proto"
	"github.com/tidwall/gjson"
	"github.com/xssnick/tonutils-go/ton"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestProtocolMatrix(t *testing.T) {
	for _, n := range []Network{
		{Name: "aptos", Protocol: "jsonrpc", URL: "https://node"},
		{Name: "ton", Protocol: "grpc", URL: "grpcs://node"},
		{Name: "ton", Protocol: "lite", URL: "adnl://192.0.2.1:1234/short"},
		{Name: "ton", Protocol: "lite", URL: "adnl://192.0.2.1/key"},
		{Name: "solana", Protocol: "ws", URL: "wss://node"},
		{Name: "sui", Protocol: "grpc", URL: "https://node/graphql"},
		{Name: "monad", Protocol: "graphql", TraceCalls: true, URL: "https://node/graphql"},
		{Name: "ethereum", Protocol: "ipc", URL: "ipc://remote/relative"},
		{Name: "ethereum", Protocol: "http", URL: "https://node", HistoryURL: "https://unused"},
	} {
		if ValidateNetwork(n) == nil {
			t.Fatalf("accepted %+v", n)
		}
	}
	for _, network := range []string{"aptos", "sui", "tron", "ton", "solana", "ethereum", "base", "arbitrum", "hyperevm", "monad", "arc", "optimism", "avalanche", "plasma", "binance", "polygon"} {
		for _, p := range Protocols(network) {
			n := Network{Name: network, Protocol: p, URL: "https://node"}
			switch p {
			case "ws":
				n.URL = "wss://node"
			case "ipc":
				n.URL = "ipc:///var/run/geth.ipc"
			case "grpc":
				n.URL = "grpcs://node:443"
			}
			if NeedsHistory(network, p) {
				n.HistoryURL = "https://separate"
			}
			if e := ValidateNetwork(n); e != nil {
				t.Fatalf("%s %s: %v", network, p, e)
			}
		}
	}
}
func TestTronWalletWireEvidence(t *testing.T) {
	to := append([]byte{65}, bytes.Repeat([]byte{0x11}, 20)...)
	a, e := anypb.New(&core.TransferContract{ToAddress: to, Amount: 9007199254740993})
	if e != nil {
		t.Fatal(e)
	}
	c := &core.Transaction_Contract{Type: core.Transaction_Contract_TransferContract, Parameter: a}
	m, e := tronMessage(c.ProtoReflect())
	if e != nil {
		t.Fatal(e)
	}
	v := gjson.ParseBytes(object(m))
	if v.Get("type").String() != "TransferContract" || v.Get("parameter.value.amount").Raw != "9007199254740993" || v.Get("parameter.value.to_address").String() != fmt.Sprintf("%x", to) {
		t.Fatal(v)
	}
}
func TestExecutionGraphQLRequiresCompleteReceipts(t *testing.T) {
	const emitter = "0x2222222222222222222222222222222222222222"
	const sig = "0xe2403640ba68fed3a2f88b7557551d1993f84b99bb10ff833f0cf8db0c5e0486"
	ctx := context.Background()
	complete := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Query, "chainID") {
			fmt.Fprint(w, `{"data":{"chainID":"0xa","block":{"number":"0xa"}}}`)
			return
		}
		var logs any
		if complete {
			logs = []any{map[string]any{"index": "0x0", "account": map[string]string{"address": emitter}, "topics": []string{sig}, "data": "0x" + strings.Repeat("0", 24) + wallet[2:] + strings.Repeat("0", 64)}}
		}
		tx := map[string]any{"hash": "hash", "from": map[string]string{"address": emitter}, "to": map[string]string{"address": emitter}, "value": "0xde0b6b3a7640000", "status": "0x1", "index": "0x0", "logs": logs}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"block": map[string]any{"hash": "block", "number": "0xa", "timestamp": 12, "transactionCount": 1, "transactions": []any{tx}}}})
	}))
	defer server.Close()
	var n Network
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"name":"optimism","url":%q,"protocol":"graphql","asset_events":{"%s":{"%s":{"topics":1,"data_words":2,"address_words":[0],"refresh_asset":true}}}}`, server.URL, emitter, sig)), &n); err != nil {
		t.Fatal(err)
	}
	src, e := NewSource(ctx, n)
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	_, ev, e := src.Read(ctx, 10, 10)
	if e != nil || len(ev) != 1 || !slices.Contains(ev[0].Addresses, wallet) || gjson.GetBytes(ev[0].Data, "receipt.status").String() != "0x1" {
		t.Fatal(ev, e)
	}
	if gjson.GetBytes(ev[0].Data, "refresh_assets.0").String() != emitter {
		t.Fatalf("missing GraphQL refresh hint: %s", ev[0].Data)
	}
	complete = false
	if _, _, e = src.Read(ctx, 10, 10); e == nil {
		t.Fatal("advanced despite missing logs")
	}
}

type geyserFixture struct {
	geyser.UnimplementedGeyserServer
	t *testing.T
}

func (g *geyserFixture) GetSlot(ctx context.Context, r *geyser.GetSlotRequest) (*geyser.GetSlotResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if !slices.Contains(md.Get("x-token"), "stream-secret") || r.GetCommitment() != geyser.CommitmentLevel_FINALIZED {
		g.t.Error("wrong gRPC metadata or commitment")
	}
	return &geyser.GetSlotResponse{Slot: 99}, nil
}
func (g *geyserFixture) Subscribe(st grpc.BidiStreamingServer[geyser.SubscribeRequest, geyser.SubscribeUpdate]) error {
	req, e := st.Recv()
	if e != nil {
		return e
	}
	if len(req.Slots) != 1 || req.GetCommitment() != geyser.CommitmentLevel_FINALIZED {
		g.t.Error("invalid stream filter")
	}
	return st.Send(&geyser.SubscribeUpdate{UpdateOneof: &geyser.SubscribeUpdate_Slot{Slot: &geyser.SubscribeUpdateSlot{Slot: 99, Status: geyser.SlotStatus_SLOT_FINALIZED}}})
}
func startTestGRPC(t *testing.T, configure func(*grpc.Server)) string {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	server := grpc.NewServer()
	configure(server)
	go server.Serve(ln)
	t.Cleanup(server.Stop)
	return "grpc://" + ln.Addr().String()
}
func TestYellowstoneNotifiesAndReadsFinalizedHTTP(t *testing.T) {
	target := startTestGRPC(t, func(s *grpc.Server) { geyser.RegisterGeyserServer(s, &geyserFixture{t: t}) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer history-secret" || r.Header.Get("x-token") != "" {
			t.Error("history authentication isolation")
		}
		var req testutil.RPCRequest
		json.NewDecoder(r.Body).Decode(&req)
		results := map[string]any{"getGenesisHash": "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d", "getSlot": 99, "getBlocks": []int{99}, "getBlock": map[string]any{"blockhash": "h", "transactions": []any{}}}
		json.NewEncoder(w).Encode(map[string]any{"id": req.ID, "jsonrpc": "2.0", "result": results[req.Method]})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	src, e := NewSource(ctx, Network{Name: "solana", Protocol: "grpc", URL: target, APIKey: "stream-secret", HistoryURL: server.URL, HistoryAPIKey: "history-secret"})
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	select {
	case <-Wakeups(src):
	case <-ctx.Done():
		t.Fatal("no finalized wakeup")
	}
	end, _, e := src.Read(ctx, 99, 99)
	if e != nil || end != 99 {
		t.Fatal(end, e)
	}
}
func TestAptosStreamIsIndependentOfHistory(t *testing.T) {
	target := startTestGRPC(t, func(s *grpc.Server) {
		s.RegisterService(&grpc.ServiceDesc{ServiceName: "aptos.indexer.v1.RawData", HandlerType: (*interface{})(nil), Streams: []grpc.StreamDesc{{StreamName: "GetTransactions", ServerStreams: true, Handler: func(_ any, stream grpc.ServerStream) error {
			var req aptosRequest
			if e := stream.RecvMsg(&req); e != nil {
				return e
			}
			md, _ := metadata.FromIncomingContext(stream.Context())
			if !slices.Contains(md.Get("authorization"), "Bearer stream") || req.Start == nil || *req.Start != 25 {
				t.Error("invalid starting version or token")
			}
			return stream.SendMsg(&aptosBatch{ChainID: proto.Uint64(1), Transactions: []*aptosVersion{{Version: 25}}})
		}}}}, struct{}{})
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer history" {
			t.Error("wrong history credential")
		}
		if r.URL.Path == "/v1" {
			fmt.Fprint(w, `{"chain_id":1,"ledger_version":"25"}`)
		} else {
			fmt.Fprint(w, `[{"version":"25","hash":"h","sender":"0x1"}]`)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	src, e := NewSource(ctx, Network{Name: "aptos", Protocol: "grpc", URL: target, HistoryURL: srv.URL, APIKey: "stream", HistoryAPIKey: "history"})
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	select {
	case <-Wakeups(src):
	case <-ctx.Done():
		t.Fatal("missing wakeup")
	}
	h, ev, e := src.Read(ctx, 25, 25)
	if e != nil || h != 25 || len(ev) != 1 {
		t.Fatal(h, ev, e)
	}
}

type ipcFixture struct{}

func (ipcFixture) ChainId() string     { return "0x1" }
func (ipcFixture) BlockNumber() string { return "0x37" }
func TestIPCReadsExecutionHeight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets")
	}
	// Darwin limits Unix socket paths to 104 bytes.
	dir, e := os.MkdirTemp("", "cs-ipc")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	ln, svc, e := rpc.StartIPCEndpoint(path, []rpc.API{{Namespace: "eth", Service: ipcFixture{}}})
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	defer svc.Stop()
	src, e := NewSource(context.Background(), Network{Name: "ethereum", URL: "ipc://" + path})
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	if h, e := src.Head(context.Background()); e != nil || h != 55 {
		t.Fatal(h, e)
	}
}
func TestBeaconSignalsUseExecutionHeights(t *testing.T) {
	st := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("topics") != "head" {
			t.Error("missing topic")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: head\ndata: {\"slot\":\"123456\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer st.Close()
	history := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		if r.Method == "eth_chainId" {
			return "0x1", nil
		}
		if r.Method == "eth_blockNumber" {
			return "0x42", nil
		}
		return nil, fmt.Errorf("unexpected method %s", r.Method)
	})
	defer history.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	src, e := NewSource(ctx, Network{Name: "ethereum", Protocol: "sse", URL: st.URL, HistoryURL: history.URL})
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	select {
	case <-Wakeups(src):
	case <-ctx.Done():
		t.Fatal("missing Beacon wakeup")
	}
	if h, e := src.Head(ctx); e != nil || h != 66 {
		t.Fatal(h, e)
	}
	if h, err := NodeHead(ctx, src, Network{Name: "ethereum", Protocol: "sse", URL: st.URL}); err != nil || h != 123456 {
		t.Fatalf("expected primary beacon slot 123456, got %d (%v)", h, err)
	}

}
func TestSuiGraphPageMustAdvance(t *testing.T) {
	seen := map[string]bool{}
	v := gjson.Parse(`{"nodes":[{}],"pageInfo":{"hasNextPage":true,"endCursor":"one"}}`)
	if c, e := graphPage(v, seen); e != nil || c != "one" {
		t.Fatal(c, e)
	}
	if _, e := graphPage(v, seen); e == nil {
		t.Fatal("repeated page cursor")
	}
}
func TestTONCenterRejectsChangedBlock(t *testing.T) {
	z := make([]byte, 32)
	hash := base64.StdEncoding.EncodeToString(z)
	id := &ton.BlockIDExt{Workchain: -1, Shard: -1 << 63, SeqNo: 42, RootHash: z, FileHash: z}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"result":{"data":"AAAA","id":{"workchain":-1,"shard":"-9223372036854775808","seqno":42,"root_hash":%q,"file_hash":%q}}}`, hash, hash)
	}))
	defer server.Close()
	s := &tonCenter{&jsonSource{n: Network{Name: "ton", URL: server.URL}, http: server.Client()}, "rest"}
	if _, e := s.GetBlockData(context.Background(), id); e == nil || !strings.Contains(e.Error(), "file hash") {
		t.Fatal(e)
	}
}

type avalancheHeadFixture struct{}

func (avalancheHeadFixture) NewHeads(ctx context.Context) (*rpc.Subscription, error) {
	n, ok := rpc.NotifierFromContext(ctx)
	if !ok {
		return nil, rpc.ErrNotificationsUnsupported
	}
	sub := n.CreateSubscription()
	return sub, n.Notify(sub.ID, map[string]string{"number": "0x65"})
}

func TestAvalancheSubscriptionOnlyWS(t *testing.T) {
	srv := rpc.NewServer()
	defer srv.Stop()
	if err := srv.RegisterName("eth", avalancheHeadFixture{}); err != nil {
		t.Fatal(err)
	}
	ws := httptest.NewServer(srv.WebsocketHandler([]string{"*"}))
	defer ws.Close()
	history := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "eth_chainId":
			return "0xa86a", nil
		case "eth_blockNumber":
			return "0x64", nil
		case "eth_getBlockByNumber":
			return map[string]any{"number": "0x64", "hash": "block100", "parentHash": "block99", "transactions": []any{}, "timestamp": "0x1"}, nil
		case "eth_getBlockReceipts", "eth_getLogs":
			return []any{}, nil
		default:
			return nil, fmt.Errorf("unexpected HTTP method %s", r.Method)
		}
	})
	defer history.Close()
	cfg := Network{Name: "avalanche", Protocol: "ws", URL: "ws" + strings.TrimPrefix(ws.URL, "http"), HistoryURL: history.URL}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	src, err := NewSource(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	select {
	case <-Wakeups(src):
	case <-ctx.Done():
		t.Fatal("no newHeads notification")
	}
	if h, err := NodeHead(ctx, src, cfg); err != nil || h != 101 {
		t.Fatal("WS height", h, err)
	}
	if h, err := src.Head(ctx); err != nil || h != 100 {
		t.Fatal("HTTP height", h, err)
	}
	if _, link, _, err := ReadSpan(ctx, "avalanche", src, Span{First: 100, Last: 100}); err != nil || link.Hash != "block100" || link.Parent != "block99" {
		t.Fatal("HTTP block", link, err)
	}
	if route, ok := LookupRoute(cfg); !ok || route.URL != history.URL || route.Protocol != "http" {
		t.Fatal("lookup route", route, ok)
	}
	wrong := testutil.RPCServer(t, func(testutil.RPCRequest) (any, error) { return "0xa869", nil })
	defer wrong.Close()
	cfg.HistoryURL = wrong.URL
	if s, err := NewSource(ctx, cfg); err == nil {
		s.Close()
		t.Fatal("accepted Fuji history")
	}
}
