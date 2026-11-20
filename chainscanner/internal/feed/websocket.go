package feed

import (
	"context"
	"net/http"
	"time"

	api "github.com/GMWalletApp/ChainScanner/api/activity/v1"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const maxClientFrame = 8 << 20

func (s *streams) websocket(w http.ResponseWriter, r *http.Request, consumer string) {
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadLimit(maxClientFrame)
	conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(75 * time.Second)) })
	closeFrame := func(code int, msg string) {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, msg), time.Now().Add(time.Second))
	}

	// Keep reading control frames while the queue is idle or the consumer is processing.
	frames := make(chan *api.ClientFrame, 1)
	go func() {
		defer cancel()
		for {
			kind, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			frame := new(api.ClientFrame)
			if kind != websocket.TextMessage || protojson.Unmarshal(raw, frame) != nil {
				closeFrame(websocket.CloseUnsupportedData, "expected a ProtoJSON text frame")
				return
			}
			select {
			case frames <- frame:
			default:
				closeFrame(websocket.ClosePolicyViolation, "too many pending commands")
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					cancel()
					return
				}
			}
		}
	}()
	recv := func() (*api.ClientFrame, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case f := <-frames:
			return f, nil
		}
	}
	send := func(f *api.ServerFrame) error {
		raw, err := protojson.Marshal(f)
		if err != nil {
			return err
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, raw)
	}
	err = s.serve(ctx, consumer, recv, send)
	if err != nil && ctx.Err() == nil {
		code := websocket.CloseInternalServerErr
		if status.Code(err) == codes.InvalidArgument {
			code = websocket.ClosePolicyViolation
		}
		if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.Unavailable {
			code = websocket.CloseTryAgainLater
		}
		// Fixed-size reasons fit the websocket control frame limit.
		closeFrame(code, status.Code(err).String())
	}
}
