package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/GMWalletApp/ChainScanner/api/activity/v1"
	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestConsumerStreamsShareDurableQueue(t *testing.T) {
	const token = "012345678901234567890123"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SeedCursor(ctx, "base", 10); err != nil {
		t.Fatal(err)
	}
	s.resolve = func(context.Context, string) (int64, error) { return 14, nil }
	h, err := ConsumerHandler(s, map[string]string{"merchant": token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Config.ReadTimeout = 150 * time.Millisecond
	srv.Start()
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/events/stream"
	if c, response, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil); err == nil || response.StatusCode != 401 {
		if c != nil {
			c.Close()
		}
		t.Fatal("unauthenticated websocket", response, err)
	}
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = ws.WriteJSON(map[string]any{"open": map[string]any{"network": "base", "replace": map[string]any{"watches": []map[string]string{{"network": "base", "address": wallet}}}}}); err != nil {
		t.Fatal(err)
	}
	readWS := func() *api.ServerFrame {
		t.Helper()
		_, b, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		f := new(api.ServerFrame)
		if err = protojson.Unmarshal(b, f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	if reg := readWS().GetReady().GetRegistrations(); len(reg) != 1 || reg[0].FromHeight != 11 {
		t.Fatal("watch boundary", reg)
	}
	commit := func(prev int64, tx string) {
		t.Helper()
		err := completeNext(s, ctx, "base", prev, prev+1, []chains.Activity{{Network: "base", Height: prev + 1, TxID: tx, Addresses: []string{wallet}, Data: json.RawMessage(`{"amount":"12345678901234567890"}`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	wsAck := func(id int64) {
		t.Helper()
		if err := ws.WriteJSON(map[string]int64{"ack": id}); err != nil {
			t.Fatal(err)
		}
	}
	commit(10, "first")
	d1 := readWS().GetDelivery()
	if d1.GetTxId() != "first" {
		t.Fatal(d1)
	}
	wsAck(d1.Id)
	commit(11, "second")
	d2 := readWS().GetDelivery()
	if d2.GetTxId() != "second" {
		t.Fatal(d2)
	}
	ws.Close() // leave second unacknowledged, resume on another protocol
	cc, err := grpc.NewClient(strings.TrimPrefix(srv.URL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	client := api.NewActivityFeedClient(cc)
	stream, err := client.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatal("unauthorized grpc", err)
	}
	auth := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	open := func() api.ActivityFeed_SubscribeClient {
		t.Helper()
		stream, err := client.Subscribe(auth)
		if err != nil {
			t.Fatal(err)
		}
		if err = stream.Send(&api.ClientFrame{Command: &api.ClientFrame_Open{Open: &api.Open{Network: "base"}}}); err != nil {
			t.Fatal(err)
		}
		if f, err := stream.Recv(); err != nil || f.GetReady() == nil {
			t.Fatal("resume", f, err)
		}
		return stream
	}
	stream = open()
	f, err := stream.Recv()
	if err != nil || f.GetDelivery().GetId() != d2.Id || f.GetDelivery().GetDataJson() != d2.DataJson {
		t.Fatal("redelivery", f, err)
	}
	if err = stream.Send(&api.ClientFrame{Command: &api.ClientFrame_Ack{Ack: d1.Id}}); err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); status.Code(err) != codes.InvalidArgument {
		t.Fatal("wrong ack", err)
	}
	stream = open()
	f, err = stream.Recv()
	if err != nil || f.GetDelivery().GetId() != d2.Id {
		t.Fatal("lost on bad ack", f, err)
	}
	if err = stream.Send(&api.ClientFrame{Command: &api.ClientFrame_Ack{Ack: d2.Id}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // outlive the server's ordinary request deadlines
	commit(12, "third")
	f, err = stream.Recv()
	if err != nil || f.GetDelivery().GetTxId() != "third" {
		t.Fatal("idle stream", f, err)
	}
	closing, err := client.Subscribe(auth)
	if err != nil {
		t.Fatal(err)
	}
	if err = closing.Send(&api.ClientFrame{Command: &api.ClientFrame_Open{Open: &api.Open{Network: "base", Replace: &api.WatchSet{}}}}); err != nil {
		t.Fatal(err)
	}
	ready, err := closing.Recv()
	ends := ready.GetReady().GetTerminations()
	if err != nil || len(ends) != 1 || ends[0].FromHeight != 11 || ends[0].ToHeight != 14 || ends[0].Completed {
		t.Fatal(ready, err)
	}
}
