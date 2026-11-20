package feed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

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
	if !chains.Supported(network) {
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
