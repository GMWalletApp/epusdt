package chains

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// RpcNode's public mainnets share a five-request/second allowance per IP.
// Space requests across chain workers, including health probes and retries.
var rpcNodeBudget = requestBudget{gate: make(chan struct{}, 1), spacing: 205 * time.Millisecond}

type requestBudget struct {
	gate    chan struct{}
	next    time.Time
	spacing time.Duration
	resume  atomic.Int64
}

func (b *requestBudget) wait(ctx context.Context) error {
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-b.gate }()
	for {
		deadline := b.next
		if resume := time.Unix(0, b.resume.Load()); resume.After(deadline) {
			deadline = resume
		}
		delay := time.Until(deadline)
		if delay <= 0 {
			break
		}
		timer := time.NewTimer(delay)

		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.next = time.Now().Add(b.spacing)
	return nil
}

type rpcNodeTransport struct{ http.RoundTripper }

func (t rpcNodeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Hostname())
	if !strings.HasPrefix(host, "public-") || !strings.HasSuffix(host, ".rpcnode.dev") {
		return t.RoundTripper.RoundTrip(req)
	}
	if err := rpcNodeBudget.wait(req.Context()); err != nil {
		return nil, err
	}
	res, err := t.RoundTripper.RoundTrip(req)
	if err == nil && res.StatusCode == http.StatusTooManyRequests {
		// The public service specifies Retry-After: 1 across its mainnets.
		rpcNodeBudget.resume.Store(time.Now().Add(time.Second).UnixNano())
	}
	return res, err
}

func (t rpcNodeTransport) CloseIdleConnections() {
	if c, ok := t.RoundTripper.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
