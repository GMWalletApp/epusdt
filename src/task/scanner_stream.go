package task

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	scanner "github.com/GMWalletApp/ChainScanner"
	api "github.com/GMWalletApp/ChainScanner/api/activity/v1"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

func runScannerFeed(ctx context.Context, c scanner.Client, network, transport string) {
	if transport == "http" {
		consumeQueue(ctx, c, func() (*scanner.Delivery, error) {
			return c.PendingNetwork(ctx, paymentConsumer, network, 15*time.Second)
		})
		return
	}
	delay := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		var err error
		if transport == "grpc" {
			err = grpcFeed(ctx, c, network)
		} else {
			err = websocketFeed(ctx, c, network)
		}
		if ctx.Err() != nil {
			return
		}
		log.Sugar.Warnf("[ChainScanner] %s event stream: %v", network, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if time.Since(started) > 2*time.Minute {
			delay = time.Second
		} else {
			delay = min(2*delay, time.Minute)
		}
	}
}

// One event is outstanding per chain. The server retains it until receipt of
// an ACK after the payment decision (including its confirmation job) commits.
func readFrames(ctx context.Context, network string, send func(*api.ClientFrame) error, recv func() (*api.ServerFrame, error)) error {
	if err := send(&api.ClientFrame{Command: &api.ClientFrame_Open{Open: &api.Open{Network: network}}}); err != nil {
		return err
	}
	f, err := recv()
	if err != nil {
		return err
	}
	if f.GetReady() == nil {
		return errors.New("missing scanner stream receipt")
	}
	for ctx.Err() == nil {
		f, err = recv()
		if err != nil {
			return err
		}
		d := f.GetDelivery()
		if d == nil || d.Id <= 0 || d.Network != network || !json.Valid([]byte(d.DataJson)) {
			return errors.New("invalid scanner delivery")
		}
		a := scanner.Activity{Network: d.Network, Height: d.Height, TxID: d.TxId, TimeMS: d.TimeMs, Confirmations: d.Confirmations, Addresses: d.Addresses, Data: json.RawMessage(d.DataJson)}
		if err := processScannerActivity(ctx, a); err != nil {
			return err
		}
		if err := send(&api.ClientFrame{Command: &api.ClientFrame_Ack{Ack: d.Id}}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func websocketFeed(ctx context.Context, c scanner.Client, network string) error {
	endpoint := "ws" + strings.TrimPrefix(c.URL, "http") + "/v1/events/stream"
	ws, res, err := websocket.DefaultDialer.DialContext(ctx, endpoint, http.Header{"Authorization": []string{"Bearer " + c.Token}})
	if err != nil {
		if res != nil {
			res.Body.Close()
			return fmt.Errorf("websocket HTTP %d", res.StatusCode)
		}
		return err
	}
	defer ws.Close()
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()
	ws.SetReadLimit(64 << 20)
	// ChainScanner sends a ping every 25 seconds, including while the chain is idle.
	touch := func() error { return ws.SetReadDeadline(time.Now().Add(90 * time.Second)) }
	if err := touch(); err != nil {
		return err
	}
	pong := ws.PingHandler()
	ws.SetPingHandler(func(s string) error {
		if err := touch(); err != nil {
			return err
		}
		return pong(s)
	})
	return readFrames(ctx, network, func(f *api.ClientFrame) error {
		b, err := protojson.Marshal(f)
		if err != nil {
			return err
		}
		ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return ws.WriteMessage(websocket.TextMessage, b)
	}, func() (*api.ServerFrame, error) {
		_, b, err := ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		f := new(api.ServerFrame)
		return f, protojson.Unmarshal(b, f)
	})
}

func grpcFeed(ctx context.Context, c scanner.Client, network string) error {
	u, err := url.Parse(c.URL)
	if err != nil {
		return err
	}
	var cred credentials.TransportCredentials = insecure.NewCredentials()
	if u.Scheme == "https" {
		cred = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			host = u.Host + ":443"
		} else {
			host = u.Host + ":80"
		}
	}
	conn, err := grpc.NewClient("passthrough:///"+host, grpc.WithTransportCredentials(cred), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<20)))
	if err != nil {
		return err
	}
	defer conn.Close()
	stream, err := api.NewActivityFeedClient(conn).Subscribe(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.Token))
	if err != nil {
		return err
	}
	return readFrames(ctx, network, stream.Send, stream.Recv)
}
