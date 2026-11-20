package scanner

import (
	"context"
	"errors"
	"google.golang.org/grpc"
)

// Protobuf projections of RawData/GetTransactions. REST supplies the decoded
// Move events and write sets; only the stream's ledger versions wake the worker.
type aptosRequest struct {
	Start *uint64 `protobuf:"varint,1,opt,name=starting_version"`
	Count *uint64 `protobuf:"varint,2,opt,name=transactions_count"`
	Batch *uint64 `protobuf:"varint,3,opt,name=batch_size"`
}

func (r *aptosRequest) Reset()       { *r = aptosRequest{} }
func (*aptosRequest) String() string { return "Aptos stream request" }
func (*aptosRequest) ProtoMessage()  {}

type aptosVersion struct {
	Version uint64 `protobuf:"varint,2,opt,name=version,proto3"`
}

func (r *aptosVersion) Reset()       { *r = aptosVersion{} }
func (*aptosVersion) String() string { return "Aptos version" }
func (*aptosVersion) ProtoMessage()  {}

type aptosBatch struct {
	Transactions []*aptosVersion `protobuf:"bytes,1,rep,name=transactions"`
	ChainID      *uint64         `protobuf:"varint,2,opt,name=chain_id"`
}

func (r *aptosBatch) Reset()       { *r = aptosBatch{} }
func (*aptosBatch) String() string { return "Aptos stream response" }
func (*aptosBatch) ProtoMessage()  {}
func (s *pairedSource) aptosStream(ctx context.Context, key string, wake func()) error {
	info, e := s.request(ctx, "/v1", nil)
	if e != nil {
		return e
	}
	if info.Get("chain_id").Int() != 1 || !info.Get("ledger_version").Exists() {
		return errors.New("expected Aptos mainnet REST ledger")
	}
	start := info.Get("ledger_version").Uint()
	next := start
	size := uint64(100)
	req := &aptosRequest{Start: &start, Batch: &size}
	if wake == nil {
		one := uint64(1)
		req.Count = &one
	}
	if key != "" {
		ctx = grpcAuth(ctx, "authorization", "Bearer "+key)
	}
	st, e := s.conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, "/aptos.indexer.v1.RawData/GetTransactions")
	if e != nil {
		return e
	}
	if e = st.SendMsg(req); e != nil {
		return e
	}
	if e = st.CloseSend(); e != nil {
		return e
	}
	for {
		var b aptosBatch
		if e = st.RecvMsg(&b); e != nil {
			return e
		}
		if b.ChainID == nil || *b.ChainID != 1 || len(b.Transactions) == 0 {
			return errors.New("invalid Aptos stream batch")
		}
		for _, tx := range b.Transactions {
			if tx == nil || tx.Version != next {
				return errors.New("gap in Aptos notification stream")
			}
			next++
		}
		if wake == nil {
			s.verifiedVersion = int64(next - 1)
			return nil
		}
		wake()
	}
}
