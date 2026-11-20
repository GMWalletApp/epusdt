package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/GMWalletApp/ChainScanner/internal/testutil"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/api"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/core"
	"google.golang.org/grpc"
)

type testWallet struct {
	api.UnimplementedWalletServer
	block atomic.Int64
}

func testBlock(h int64) *api.BlockExtention {
	return &api.BlockExtention{Blockid: make([]byte, 32), BlockHeader: &core.BlockHeader{RawData: &core.BlockHeaderRaw{Number: h}}}
}
func (*testWallet) GetNowBlock2(context.Context, *api.EmptyMessage) (*api.BlockExtention, error) {
	return testBlock(102), nil
}
func (s *testWallet) GetBlockByNum2(_ context.Context, n *api.NumberMessage) (*api.BlockExtention, error) {
	s.block.Store(n.Num)
	return testBlock(n.Num), nil
}

func TestTronMixedTransportReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wallet := &testWallet{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := grpc.NewServer()
	api.RegisterWalletServer(service, wallet)
	go service.Serve(ln)
	defer service.Stop()

	native := func(read *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := int64(102)
			switch r.URL.Path {
			case "/wallet/getnowblock":
			case "/wallet/getblockbynum":
				var in struct{ Num int64 }
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
					return
				}
				h = in.Num
				read.Store(h)
			default:
				t.Error(r.URL.Path)
				return
			}
			fmt.Fprintf(w, `{"blockID":"%064x","block_header":{"raw_data":{"number":%d}},"transactions":[]}`, h, h)
		}))
	}
	var direct, history atomic.Int64
	a := native(&direct)
	defer a.Close()
	c := native(&history)
	defer c.Close()
	rpc := testutil.RPCServer(t, func(r testutil.RPCRequest) (any, error) {
		switch r.Method {
		case "eth_chainId":
			return "0x2b6653dc", nil
		case "eth_blockNumber":
			return "0x66", nil
		case "eth_getBlockByNumber":
			return map[string]string{"hash": fmt.Sprintf("0x%064x", 102)}, nil
		}
		return nil, fmt.Errorf("unsupported method %s", r.Method)
	})
	defer rpc.Close()
	p := NewSourcePool(ctx, "tron")
	defer p.Close()
	if err = p.SetNodes([]chains.Network{
		{Name: "tron", Protocol: "http", URL: a.URL},
		{Name: "tron", Protocol: "grpc", URL: "grpc://" + ln.Addr().String()},
		{Name: "tron", Protocol: "jsonrpc", URL: rpc.URL, HistoryURL: c.URL},
	}); err != nil {
		t.Fatal(err)
	}
	end, _, err := p.Read(ctx, 100, 102)
	if err != nil || end != 102 || direct.Load() != 100 || wallet.block.Load() != 101 || history.Load() != 102 {
		t.Fatal(end, err, direct.Load(), wallet.block.Load(), history.Load())
	}
}
