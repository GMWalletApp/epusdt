package scanner

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/fbsobreira/gotron-sdk/pkg/proto/api"
	"github.com/tidwall/gjson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

type tronBlockClient interface {
	GetNowBlock2(context.Context, *api.EmptyMessage, ...grpc.CallOption) (*api.BlockExtention, error)
	GetBlockByNum2(context.Context, *api.NumberMessage, ...grpc.CallOption) (*api.BlockExtention, error)
	GetTransactionInfoByBlockNum(context.Context, *api.NumberMessage, ...grpc.CallOption) (*api.TransactionInfoList, error)
}
type tronGRPC struct {
	conn   *grpc.ClientConn
	wallet tronBlockClient
	key    string
}

func newTronGRPC(ctx context.Context, n Network, u *url.URL) (Source, error) {
	c, e := dialGRPC(u)
	if e != nil {
		return nil, e
	}
	var wallet tronBlockClient = api.NewWalletClient(c)
	if n.Protocol == "solidity-grpc" {
		wallet = api.NewWalletSolidityClient(c)
	}
	s := &tronGRPC{c, wallet, n.APIKey}
	if _, e = s.Head(ctx); e != nil {
		c.Close()
		return nil, e
	}
	return s, nil
}
func (s *tronGRPC) auth(ctx context.Context) context.Context {
	if s.key != "" {
		return metadata.AppendToOutgoingContext(ctx, "tron-pro-api-key", s.key)
	}
	return ctx
}
func (s *tronGRPC) Close() { s.conn.Close() }
func (s *tronGRPC) Head(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(s.auth(ctx), 15*time.Second)
	defer cancel()
	b, e := s.wallet.GetNowBlock2(ctx, &api.EmptyMessage{})
	if e != nil {
		return 0, e
	}
	if len(b.GetBlockid()) != 32 || b.GetBlockHeader().GetRawData() == nil {
		return 0, errors.New("missing TRON block header")
	}
	return b.GetBlockHeader().GetRawData().GetNumber(), nil
}
func (s *tronGRPC) Read(ctx context.Context, h, head int64) (int64, []Activity, error) {
	ctx, cancel := context.WithTimeout(s.auth(ctx), 40*time.Second)
	defer cancel()
	req := &api.NumberMessage{Num: h}
	b, e := s.wallet.GetBlockByNum2(ctx, req)
	if e != nil {
		return 0, nil, e
	}
	header := b.GetBlockHeader().GetRawData()
	if header == nil || len(b.GetBlockid()) != 32 || header.GetNumber() != h {
		return 0, nil, errors.New("missing TRON block")
	}
	if len(b.Transactions) == 0 {
		return h, nil, nil
	}
	rs, e := s.wallet.GetTransactionInfoByBlockNum(ctx, req)
	if e != nil {
		return 0, nil, e
	}
	txs := make([]gjson.Result, 0, len(b.Transactions))
	for _, tx := range b.Transactions {
		if tx.GetTransaction().GetRawData() == nil || len(tx.GetTxid()) != 32 {
			return 0, nil, errors.New("incomplete TRON transaction")
		}
		m, e := tronMessage(tx.Transaction.ProtoReflect())
		if e != nil {
			return 0, nil, e
		}
		m["txID"] = hex.EncodeToString(tx.Txid)
		txs = append(txs, gjson.ParseBytes(object(m)))
	}
	receipts := make([]any, 0, len(rs.TransactionInfo))
	for _, r := range rs.TransactionInfo {
		m, e := tronMessage(r.ProtoReflect())
		if e != nil {
			return 0, nil, e
		}
		receipts = append(receipts, m)
	}
	return tronActivities(h, header.Timestamp, txs, gjson.ParseBytes(object(receipts)))
}

// Wallet HTTP represents protobuf bytes as hex and embeds decoded contract
// parameters. Keep the same evidence shape for consumers on both interfaces.
func tronMessage(m protoreflect.Message) (map[string]any, error) {
	result := map[string]any{}
	if a, ok := m.Interface().(*anypb.Any); ok {
		decoded, e := a.UnmarshalNew()
		if e != nil {
			return nil, e
		}
		value, e := tronMessage(decoded.ProtoReflect())
		return map[string]any{"type_url": a.TypeUrl, "value": value}, e
	}
	var err error
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		var value any
		switch {
		case f.IsMap():
			values := map[string]any{}
			v.Map().Range(func(k protoreflect.MapKey, x protoreflect.Value) bool {
				values[fmt.Sprint(k.Interface())], err = tronScalar(f.MapValue(), x)
				return err == nil
			})
			value = values
		case f.IsList():
			list := v.List()
			items := make([]any, list.Len())
			for i := range items {
				items[i], err = tronScalar(f, list.Get(i))
				if err != nil {
					break
				}
			}
			value = items
		default:
			value, err = tronScalar(f, v)
		}
		result[string(f.Name())] = value
		return err == nil
	})
	return result, err
}
func tronScalar(f protoreflect.FieldDescriptor, v protoreflect.Value) (any, error) {
	switch f.Kind() {
	case protoreflect.MessageKind:
		return tronMessage(v.Message())
	case protoreflect.BytesKind:
		return hex.EncodeToString(v.Bytes()), nil
	case protoreflect.EnumKind:
		if x := f.Enum().Values().ByNumber(v.Enum()); x != nil {
			return string(x.Name()), nil
		}
		return int32(v.Enum()), nil
	}
	return v.Interface(), nil
}
