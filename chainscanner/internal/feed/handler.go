package feed

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

// Feed can be backed by a local Store or by a remote authenticated Client.
type Feed interface {
	Height(context.Context, string) (int64, error)
	Subscribe(context.Context, string, []Watch) error
	Pending(context.Context, string) (*Delivery, error)
	Ack(context.Context, string, int64) error
}

type subscriptionStore interface {
	Feed
	SubscribeFrom(context.Context, string, []Watch) ([]Registration, error)
	PendingNetwork(context.Context, string, string, time.Duration) (*Delivery, error)
}

func Handler(s subscriptionStore, consumers map[string]string, configured func(string) bool) (http.Handler, error) {
	tokens := map[[32]byte]string{}
	for name, token := range consumers {
		key := sha256.Sum256([]byte(token))
		if name == "" || len(token) < 24 || tokens[key] != "" {
			return nil, errors.New("each consumer needs a distinct token of at least 24 bytes")
		}
		tokens[key] = name
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		consumer := tokens[sha256.Sum256([]byte(token))]
		if !ok || consumer == "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		ctx := r.Context()
		switch {
		case r.Method == "PUT" && (r.URL.Path == "/v1/subscriptions" || r.URL.Path == "/v2/subscriptions"):
			var watches []Watch
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
			if err := dec.Decode(&watches); err != nil || watches == nil {
				http.Error(w, "expected address list", 400)
				return
			}
			for _, v := range watches {
				if r.URL.Path == "/v2/subscriptions" {
					if !configured(v.Network) {
						http.Error(w, "network has no configured node", 400)
						return
					}
				}
				if _, err := chains.Canonical(v.Network, v.Address); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
			}
			if r.URL.Path == "/v2/subscriptions" {
				rows, err := s.SubscribeFrom(ctx, consumer, watches)
				if errors.Is(err, ErrCursorNotReady) {
					http.Error(w, err.Error(), 409)
					return
				}
				if err != nil {
					http.Error(w, "subscription unavailable", 503)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(rows)
				return
			}
			if err := s.Subscribe(ctx, consumer, watches); err != nil {
				http.Error(w, "subscription unavailable", 503)
				return
			}
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/heights/"):
			h, err := s.Height(ctx, strings.TrimPrefix(r.URL.Path, "/v1/heights/"))
			if err != nil {
				http.Error(w, "cursor unavailable", 503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(h)
			return
		case r.Method == "GET" && (r.URL.Path == "/v1/events/next" || r.URL.Path == "/v2/events/next"):
			network := ""
			if r.URL.Path == "/v2/events/next" {
				network = r.URL.Query().Get("network")
				if !chains.Supported(network) {
					http.Error(w, "unknown network", 400)
					return
				}
			}
			var d *Delivery
			var err error
			if seconds := r.URL.Query().Get("wait"); seconds != "" {
				n, e := strconv.Atoi(seconds)
				if e != nil || n < 1 || n > 20 {
					http.Error(w, "wait must be 1..20 seconds", 400)
					return
				}
				d, err = s.PendingNetwork(ctx, consumer, network, time.Duration(n)*time.Second)
			} else {
				d, err = s.PendingNetwork(ctx, consumer, network, 0)
			}
			if err != nil {
				http.Error(w, "queue unavailable", 503)
				return
			}
			if d != nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(d)
				return
			}
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/events/") && strings.HasSuffix(r.URL.Path, "/ack"):
			id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/events/"), "/ack"), 10, 64)
			if err != nil || id < 1 {
				http.Error(w, "invalid delivery id", 400)
				return
			}
			if err = s.Ack(ctx, consumer, id); err != nil {
				http.Error(w, "queue unavailable", 503)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), nil
}
