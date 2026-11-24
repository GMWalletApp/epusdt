package chains

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/gorilla/websocket"
	geyser "github.com/rpcpool/yellowstone-grpc/examples/golang/proto"
	"github.com/tidwall/gjson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type pairedSource struct {
	*jsonSource
	verifiedVersion int64
	wsHeight        atomic.Int64
	tip             *rpc.Client
	conn            *grpc.ClientConn
	indexer         *graphSource
}

func (s *pairedSource) Close() {
	s.jsonSource.Close()
	if s.tip != nil {
		s.tip.Close()
	}
	if s.conn != nil {
		s.conn.Close()
	}
}
func newPaired(ctx context.Context, n Network, u *url.URL, p string) (Source, error) {
	history := Network{Name: n.Name, URL: n.HistoryURL, APIKey: n.HistoryAPIKey, TraceCalls: n.TraceCalls}
	history.AssetEvents = n.AssetEvents
	src, err := NewSource(ctx, history)
	if err != nil {
		return nil, err
	}
	s := &pairedSource{jsonSource: src.(*jsonSource)}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if n.Name == "avalanche" && p == "ws" {
		s.heads = startSignals(ctx, func(ctx context.Context, wake func()) error {
			var opts []rpc.ClientOption
			if n.APIKey != "" {
				opts = append(opts, rpc.WithHeader("Authorization", "Bearer "+n.APIKey))
			}
			client, err := rpc.DialOptions(ctx, n.URL, opts...)
			if err != nil {
				return err
			}
			defer client.Close()
			return evmHeads(ctx, client, func(height int64) {
				s.wsHeight.Store(height)
				wake()
			})
		})
	} else if n.Name == "aptos" && p == "graphql" {
		s.indexer = &graphSource{&jsonSource{n: n, http: s.http}}
		if _, err = s.Head(probe); err != nil {
			return nil, err
		}
	} else if p == "sse" {
		if err = beaconHeads(probe, n, nil); err != nil {
			return nil, err
		}
		s.heads = startSignals(ctx, func(ctx context.Context, wake func()) error {
			return beaconHeads(ctx, n, func(int64) bool { wake(); return false })
		})
	} else if n.Name == "tron" {
		opts := []rpc.ClientOption{rpc.WithHTTPClient(s.http)}
		if n.APIKey != "" {
			opts = append(opts, rpc.WithHeader("TRON-PRO-API-KEY", n.APIKey))
		}
		s.tip, err = rpc.DialOptions(probe, n.URL, opts...)
		if err != nil {
			return nil, err
		}
		var id string
		if err = s.tip.CallContext(probe, &id, "eth_chainId"); err != nil {
			return nil, err
		}
		if id != "0x2b6653dc" {
			return nil, errors.New("expected TRON mainnet")
		}
	} else {
		var stream func(context.Context, func()) error
		switch p {
		case "ws":
			// Validate the endpoint and subscription synchronously, including on health probes.
			check, _, e := solanaSocket(probe, n)
			if e != nil {
				return nil, e
			}
			check.Close()
			stream = func(ctx context.Context, wake func()) error {
				return solanaRoots(ctx, n, func(int64) bool { wake(); return false })
			}
		case "grpc":
			s.conn, err = dialGRPC(u)
			if err != nil {
				return nil, err
			}
			if n.Name == "solana" {
				c := geyser.NewGeyserClient(s.conn)
				level := geyser.CommitmentLevel_FINALIZED
				if _, err = c.GetSlot(grpcAuth(probe, "x-token", n.APIKey), &geyser.GetSlotRequest{Commitment: &level}); err != nil {
					return nil, err
				}
				stream = func(ctx context.Context, wake func()) error {
					st, e := c.Subscribe(grpcAuth(ctx, "x-token", n.APIKey))
					if e != nil {
						return e
					}
					if e = st.Send(&geyser.SubscribeRequest{Slots: map[string]*geyser.SubscribeRequestFilterSlots{"roots": {FilterByCommitment: proto.Bool(true)}}, Commitment: &level}); e != nil {
						return e
					}
					for {
						msg, e := st.Recv()
						if e != nil {
							return e
						}
						if msg.GetPing() != nil {
							if e = st.Send(&geyser.SubscribeRequest{Ping: &geyser.SubscribeRequestPing{Id: 1}}); e != nil {
								return e
							}
						}
						if v := msg.GetSlot(); v != nil && v.Status == geyser.SlotStatus_SLOT_FINALIZED {
							wake()
						}
					}
				}
			} else {
				stream = func(ctx context.Context, wake func()) error { return s.aptosStream(ctx, n.APIKey, wake) }
				if err = s.aptosStream(probe, n.APIKey, nil); err != nil {
					return nil, err
				}
			}
		}
		s.heads = startSignals(ctx, stream)
	}
	ok = true
	return s, nil
}
func grpcAuth(ctx context.Context, header, key string) context.Context {
	if key == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, header, key)
}
func (s *pairedSource) Head(ctx context.Context) (int64, error) {
	if s.indexer != nil {
		indexed, e := s.indexer.aptosVersion(ctx)
		if e != nil {
			return 0, e
		}
		info, e := s.request(ctx, "/v1", nil)
		if e != nil {
			return 0, e
		}
		if info.Get("chain_id").Int() != 1 {
			return 0, errors.New("expected Aptos mainnet history")
		}
		h, e := strconv.ParseInt(info.Get("ledger_version").String(), 10, 64)
		return min(indexed, h), e
	}
	if s.tip == nil {
		return s.jsonSource.Head(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var tip string
	if err := s.tip.CallContext(ctx, &tip, "eth_blockNumber"); err != nil {
		return 0, err
	}
	return strconv.ParseInt(tip, 0, 64)
}
func (s *pairedSource) Read(ctx context.Context, next, head int64) (int64, []Activity, error) {
	if s.tip == nil {
		return s.jsonSource.Read(ctx, next, head)
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	var b struct{ Hash string }
	if err := s.tip.CallContext(ctx, &b, "eth_getBlockByNumber", fmt.Sprintf("0x%x", next), false); err != nil {
		return 0, nil, err
	}
	if !strings.HasPrefix(b.Hash, "0x") || len(b.Hash) != 66 {
		return 0, nil, errors.New("invalid TRON JSON-RPC block hash")
	}
	return s.tronBlock(ctx, next, b.Hash[2:])
}
func solanaSocket(ctx context.Context, n Network) (*websocket.Conn, string, error) {
	hdr := http.Header{}
	if n.APIKey != "" {
		hdr.Set("Authorization", "Bearer "+n.APIKey)
	}
	c, _, err := websocket.DefaultDialer.DialContext(ctx, n.URL, hdr)
	if err != nil {
		return nil, "", err
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	c.SetReadLimit(64 << 10)
	deadline := time.Now().Add(10 * time.Second)
	c.SetReadDeadline(deadline)
	c.SetWriteDeadline(deadline)
	if err = c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "rootSubscribe", "params": []any{}}); err != nil {
		c.Close()
		return nil, "", err
	}
	_, b, err := c.ReadMessage()
	v := gjson.ParseBytes(b)
	id := v.Get("result")
	if err != nil || id.Type != gjson.Number || v.Get("id").Int() != 1 {
		c.Close()
		return nil, "", errors.New("Solana rootSubscribe failed")
	}
	c.SetReadDeadline(time.Time{})
	return c, id.Raw, nil
}
func solanaRoots(ctx context.Context, n Network, onRoot func(int64) bool) error {
	c, id, e := solanaSocket(ctx, n)
	if e != nil {
		return e
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	for {
		_, b, e := c.ReadMessage()
		if e != nil {
			return e
		}
		v := gjson.ParseBytes(b)
		if v.Get("method").String() == "rootNotification" && v.Get("params.subscription").Raw == id && v.Get("params.result").Type == gjson.Number {
			slot, err := strconv.ParseInt(v.Get("params.result").Raw, 10, 64)
			if err == nil && slot >= 0 && onRoot(slot) {
				return nil
			}
		}
	}
}

func (s *graphSource) aptosVersion(ctx context.Context) (int64, error) {
	r, e := s.query(ctx, "{ledger_infos{chain_id} processor_status(order_by:{last_success_version:desc},limit:1){last_success_version}}", nil)
	if e != nil {
		return 0, e
	}
	if r.Get("ledger_infos.0.chain_id").Int() != 1 {
		return 0, errors.New("expected Aptos mainnet indexer")
	}
	return strconv.ParseInt(r.Get("processor_status.0.last_success_version").String(), 10, 64)
}
