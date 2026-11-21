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
	if res.StatusCode == 409 {
		return ErrCursorNotReady
	}
	if res.StatusCode != 200 && res.StatusCode != 202 {
		return fmt.Errorf("scanner HTTP %d", res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}
func (c Client) Ack(ctx context.Context, _ string, id int64) error {
	return c.request(ctx, "POST", fmt.Sprintf("/v1/events/%d/ack", id), nil, nil)
}

func (c Client) Height(ctx context.Context, network string) (int64, error) {
	var height int64
	err := c.request(ctx, "GET", "/v1/heights/"+network, nil, &height)
	return height, err
}

func (c Client) Subscribe(ctx context.Context, _ string, w []Watch) (SubscriptionReceipt, error) {
	if w == nil {
		w = []Watch{}
	}
	var result SubscriptionReceipt
	err := c.request(ctx, "PUT", "/v1/subscriptions", w, &result)
	if err == nil && (result.Registrations == nil || result.Terminations == nil) {
		return result, errors.New("invalid subscription receipt")
	}
	return result, err
}

func (c Client) PendingNetwork(ctx context.Context, _ string, network string, wait time.Duration) (*Delivery, error) {
	if !chains.Supported(network) {
		return nil, errors.New("unknown network")
	}
	path := "/v1/events/next?network=" + network
	if wait > 0 {
		path += fmt.Sprintf("&wait=%d", max(1, min(20, int(wait/time.Second))))
	}
	var d *Delivery
	err := c.request(ctx, "GET", path, nil, &d)
	return d, err
}
