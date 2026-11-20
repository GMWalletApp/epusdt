package feed

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"time"

	api "github.com/GMWalletApp/ChainScanner/api/activity/v1"
	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type streams struct {
	api.UnimplementedActivityFeedServer
	store      subscriptionStore
	configured func(string) bool
	tokens     map[[32]byte]string
}

func (s *streams) consumer(header string) string {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return ""
	}
	return s.tokens[sha256.Sum256([]byte(token))]
}

func (s *streams) Subscribe(stream grpc.BidiStreamingServer[api.ClientFrame, api.ServerFrame]) error {
	ctx := stream.Context()
	auth := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(auth) != 1 || s.consumer(auth[0]) == "" {
		return status.Error(codes.Unauthenticated, "invalid consumer token")
	}
	return s.serve(ctx, s.consumer(auth[0]), stream.Recv, stream.Send)
}

func (s *streams) register(ctx context.Context, consumer string, set *api.WatchSet) (*api.Ready, error) {
	ready := new(api.Ready)
	if set == nil {
		return ready, nil
	}
	watches := make([]Watch, 0, len(set.Watches))
	for _, w := range set.Watches {
		if !s.configured(w.GetNetwork()) {
			return nil, status.Error(codes.InvalidArgument, "watch network has no configured node")
		}
		if _, err := chains.Canonical(w.GetNetwork(), w.GetAddress()); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		watches = append(watches, Watch{Network: w.Network, Address: w.Address})
	}
	rows, err := s.store.SubscribeFrom(ctx, consumer, watches)
	if errors.Is(err, ErrCursorNotReady) {
		return nil, status.Error(codes.FailedPrecondition, "network cursor has not initialized")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "subscription unavailable")
	}
	for _, r := range rows {
		ready.Registrations = append(ready.Registrations, &api.Registration{Network: r.Network, Address: r.Address, FromHeight: r.FromHeight})
	}
	return ready, nil
}

func (s *streams) serve(ctx context.Context, consumer string, recv func() (*api.ClientFrame, error), send func(*api.ServerFrame) error) error {
	frame, err := recv()
	if err != nil {
		return err
	}
	open := frame.GetOpen()
	if open == nil || !chains.Supported(open.Network) || !s.configured(open.Network) {
		return status.Error(codes.InvalidArgument, "open requires a configured network")
	}
	ready, err := s.register(ctx, consumer, open.Replace)
	if err != nil {
		return err
	}
	if err = send(&api.ServerFrame{Result: &api.ServerFrame_Ready{Ready: ready}}); err != nil {
		return err
	}
	for ctx.Err() == nil {
		d, err := s.store.PendingNetwork(ctx, consumer, open.Network, 20*time.Second)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return status.Error(codes.Unavailable, "queue unavailable")
		}
		if d == nil {
			continue
		}
		a := d.Activity
		out := &api.Delivery{Id: d.ID, Network: a.Network, Height: a.Height, TxId: a.TxID, TimeMs: a.TimeMS, Confirmations: a.Confirmations, Addresses: a.Addresses, DataJson: string(a.Data)}
		if err = send(&api.ServerFrame{Result: &api.ServerFrame_Delivery{Delivery: out}}); err != nil {
			return err
		}
		frame, err = recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if frame.GetAck() != d.ID {
			return status.Error(codes.InvalidArgument, "ack must match the outstanding delivery id")
		}
		if err = s.store.Ack(ctx, consumer, d.ID); err != nil {
			return status.Error(codes.Unavailable, "acknowledgement unavailable")
		}
	}
	return ctx.Err()
}
