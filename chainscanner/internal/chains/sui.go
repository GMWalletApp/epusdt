package chains

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	pb "github.com/block-vision/sui-go-sdk/pb/sui/rpc/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type suiSource struct {
	conn   *grpc.ClientConn
	ledger pb.LedgerServiceClient
	heads  *headSignals
	key    string
}

func newSui(ctx context.Context, n Network, u *url.URL, discovered, watch bool) (Source, error) {
	var opts []grpc.DialOption
	if discovered {
		opts = append(opts, grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) { return dialPublic(ctx, "tcp", addr) }))
	}
	c, err := dialGRPC(u, opts...)
	if err != nil {
		return nil, err
	}
	s := &suiSource{conn: c, ledger: pb.NewLedgerServiceClient(c), key: n.APIKey}
	if _, err = s.Head(ctx); err != nil {
		c.Close()
		return nil, err
	}
	if !watch {
		return s, nil
	}
	s.heads = startSignals(ctx, func(ctx context.Context, wake func()) error {
		stream, err := pb.NewSubscriptionServiceClient(c).SubscribeCheckpoints(s.auth(ctx), &pb.SubscribeCheckpointsRequest{ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"sequence_number"}}})
		if err != nil {
			return err
		}
		for {
			if _, err = stream.Recv(); err != nil {
				return err
			}
			wake()
		}
	})
	return s, nil
}
func dialGRPC(u *url.URL, options ...grpc.DialOption) (*grpc.ClientConn, error) {
	var creds credentials.TransportCredentials
	switch u.Scheme {
	case "https", "grpcs":
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	case "http", "grpc":
		creds = insecure.NewCredentials()
	default:
		return nil, errors.New("expected a gRPC endpoint")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" {
		return nil, errors.New("gRPC endpoint must be host and port")
	}
	host := u.Host
	if u.Port() == "" {
		port := "443"
		if u.Scheme == "http" || u.Scheme == "grpc" {
			port = "80"
		}
		host += ":" + port
	}
	target := "dns:///" + host
	if len(options) > 0 {
		target = "passthrough:///" + host
	}
	options = append(options, grpc.WithTransportCredentials(creds), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<20)))
	return grpc.NewClient(target, options...)
}
func (s *suiSource) auth(ctx context.Context) context.Context {
	if s.key != "" {
		return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+s.key)
	}
	return ctx
}
func (s *suiSource) wakeups() <-chan struct{} {
	if s.heads == nil {
		return nil
	}
	return s.heads.ch
}
func (s *suiSource) Close() {
	if s.heads != nil {
		s.heads.stop()
	}
	s.conn.Close()
}
func (s *suiSource) Head(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	r, err := s.ledger.GetServiceInfo(s.auth(ctx), &pb.GetServiceInfoRequest{})
	if err != nil {
		return 0, err
	}
	if r.GetChain() != "mainnet" || r.CheckpointHeight == nil {
		return 0, fmt.Errorf("expected Sui mainnet with a checkpoint, got %q", r.GetChain())
	}
	return int64(r.GetCheckpointHeight()), nil
}

var suiTxFields = []string{"digest", "transaction.sender", "transaction.gas_payment.owner", "effects.status", "effects.changed_objects", "balance_changes", "events.events"}

func (s *suiSource) Read(ctx context.Context, h, head int64) (int64, []Activity, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	fields := []string{"sequence_number", "summary.timestamp", "contents.transactions.transaction"}
	for _, f := range suiTxFields {
		fields = append(fields, "transactions."+f)
	}
	r, err := s.ledger.GetCheckpoint(s.auth(ctx), &pb.GetCheckpointRequest{CheckpointId: &pb.GetCheckpointRequest_SequenceNumber{SequenceNumber: uint64(h)}, ReadMask: &fieldmaskpb.FieldMask{Paths: fields}})
	if err != nil {
		return 0, nil, err
	}
	cp := r.GetCheckpoint()
	if cp == nil || cp.SequenceNumber == nil || int64(cp.GetSequenceNumber()) != h || cp.GetSummary().GetTimestamp() == nil || cp.GetContents() == nil || len(cp.GetContents().GetTransactions()) != len(cp.GetTransactions()) {
		return 0, nil, errors.New("incomplete Sui checkpoint")
	}
	events := make([]Activity, 0, len(cp.GetTransactions()))
	for i, tx := range cp.GetTransactions() {
		if tx.GetDigest() == "" || tx.GetDigest() != cp.GetContents().GetTransactions()[i].GetTransaction() || tx.GetEffects().GetStatus() == nil || tx.GetEffects().GetStatus().Success == nil {
			return 0, nil, errors.New("incomplete Sui transaction")
		}
		e, err := suiActivity(tx, h, cp.GetSummary().GetTimestamp().AsTime().UnixMilli())
		if err != nil {
			return 0, nil, err
		}
		events = append(events, e)
	}
	return h, events, nil
}
func suiActivity(tx *pb.ExecutedTransaction, h, stamp int64) (Activity, error) {
	b, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(tx)
	if err != nil {
		return Activity{}, err
	}
	e := Activity{Network: "sui", Height: h, TxID: tx.GetDigest(), TimeMS: stamp, Data: b}
	index := func(a string) { addAddress(&e.Addresses, "sui", a) }
	index(tx.GetTransaction().GetSender())
	index(tx.GetTransaction().GetGasPayment().GetOwner())
	for _, c := range tx.GetBalanceChanges() {
		index(c.GetAddress())
	}
	for _, obj := range tx.GetEffects().GetChangedObjects() {
		index(obj.GetObjectId())
		index(obj.GetInputOwner().GetAddress())
		index(obj.GetOutputOwner().GetAddress())
	}
	for _, v := range tx.GetEvents().GetEvents() {
		index(v.GetSender())
		index(v.GetPackageId())
	}
	return e, nil
}

// SuiTransaction retrieves finalized evidence for an explicit transaction
// lookup using the same fields as the checkpoint scanner.
func SuiTransaction(ctx context.Context, n Network, digest string) (Activity, error) {
	if n.Name != "sui" {
		return Activity{}, errors.New("expected Sui network")
	}
	src, err := NewSource(ctx, n)
	if err != nil {
		return Activity{}, err
	}
	defer src.Close()
	if g, ok := src.(*graphSource); ok {
		r, e := g.query(ctx, `query($id:String!){transaction(digest:$id){`+suiGraphTx+`}}`, map[string]any{"id": digest})
		if e != nil {
			return Activity{}, e
		}
		if r.Get("transaction.digest").String() != digest {
			return Activity{}, errors.New("Sui digest mismatch")
		}
		ev, e := g.suiGraphActivity(ctx, r.Get("transaction"))
		if e != nil {
			return Activity{}, e
		}
		tip, e := g.Head(ctx)
		ev.Confirmations = tip - ev.Height + 1
		return ev, e
	}
	s := src.(*suiSource)
	ev, err := s.transaction(ctx, digest, append([]string{"checkpoint", "timestamp"}, suiTxFields...))
	if err != nil {
		return Activity{}, err
	}
	tip, err := s.Head(ctx)
	ev.Confirmations = tip - ev.Height + 1
	return ev, err
}

func (s *suiSource) transaction(ctx context.Context, digest string, fields []string) (Activity, error) {
	tx, err := s.finalizedTransaction(ctx, digest, fields)
	if err != nil {
		return Activity{}, err
	}
	if tx.GetTimestamp() == nil {
		return Activity{}, errors.New("missing Sui checkpoint timestamp")
	}
	return suiActivity(tx, int64(tx.GetCheckpoint()), tx.GetTimestamp().AsTime().UnixMilli())
}

func (s *suiSource) finalizedTransaction(ctx context.Context, digest string, fields []string) (*pb.ExecutedTransaction, error) {
	res, err := s.ledger.GetTransaction(s.auth(ctx), &pb.GetTransactionRequest{Digest: &digest, ReadMask: &fieldmaskpb.FieldMask{Paths: fields}})
	if err != nil {
		return nil, err
	}
	tx := res.GetTransaction()
	if tx.GetDigest() != digest || tx.Checkpoint == nil {
		return nil, errors.New("missing finalized Sui transaction")
	}
	return tx, nil
}
