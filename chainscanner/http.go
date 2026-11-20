package scanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Feed can be backed by a local Store or by a remote authenticated Client.
type Feed interface {
	Height(context.Context, string) (int64, error)
	Subscribe(context.Context, string, []Watch) error
	Pending(context.Context, string) (*Delivery, error)
	Ack(context.Context, string, int64) error
}

func Handler(s *Store, consumers map[string]string) (http.Handler, error) {
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
				if _, err := Canonical(v.Network, v.Address); err != nil {
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
				if !Supported(network) {
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
				d, err = s.pending(ctx, consumer, network)
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

var ErrAPINotFound = errors.New("scanner endpoint not found")

type Client struct{ URL, Token string }

var transport = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (c Client) request(ctx context.Context, method, path string, data any, out any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", "application/json")
	res, err := transport.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 204 {
		return nil
	}
	if res.StatusCode == 404 {
		return ErrAPINotFound
	}
	if res.StatusCode == 409 {
		return ErrCursorNotReady
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("scanner HTTP %d", res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}
func (c Client) Subscribe(ctx context.Context, _ string, w []Watch) error {
	if w == nil {
		w = []Watch{}
	}
	return c.request(ctx, "PUT", "/v1/subscriptions", w, nil)
}
func (c Client) Pending(ctx context.Context, _ string) (*Delivery, error) {
	var d *Delivery
	err := c.request(ctx, "GET", "/v1/events/next", nil, &d)
	return d, err
}
func (c Client) Ack(ctx context.Context, _ string, id int64) error {
	return c.request(ctx, "POST", fmt.Sprintf("/v1/events/%d/ack", id), nil, nil)
}

func (c Client) Height(ctx context.Context, network string) (int64, error) {
	var height int64
	err := c.request(ctx, "GET", "/v1/heights/"+network, nil, &height)
	return height, err
}

func (c Client) SubscribeFrom(ctx context.Context, _ string, w []Watch) ([]Registration, error) {
	if w == nil {
		w = []Watch{}
	}
	var rows []Registration
	err := c.request(ctx, "PUT", "/v2/subscriptions", w, &rows)
	return rows, err
}

func (c Client) PendingWait(ctx context.Context, _ string, wait time.Duration) (*Delivery, error) {
	var d *Delivery
	err := c.request(ctx, "GET", fmt.Sprintf("/v1/events/next?wait=%d", max(1, min(20, int(wait/time.Second)))), nil, &d)
	return d, err
}

func (c Client) PendingNetwork(ctx context.Context, _ string, network string, wait time.Duration) (*Delivery, error) {
	if !Supported(network) {
		return nil, errors.New("unknown network")
	}
	path := "/v2/events/next?network=" + network
	if wait > 0 {
		path += fmt.Sprintf("&wait=%d", max(1, min(20, int(wait/time.Second))))
	}
	var d *Delivery
	err := c.request(ctx, "GET", path, nil, &d)
	return d, err
}
