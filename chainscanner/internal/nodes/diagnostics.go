package nodes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/ethereum/go-ethereum/rpc"
	"google.golang.org/grpc/status"
)

var errNodeBehind = errors.New("node has not confirmed this range")

// Provider responses can echo credentials in paths, headers or bodies.
// Preserve machine-readable codes; never interpolate an arbitrary provider message.
func failureReason(err error) string {
	var httpErr rpc.HTTPError
	var rpcErr rpc.Error
	var networkError net.Error
	switch {
	case errors.Is(err, errNodeBehind):
		return errNodeBehind.Error()
	case errors.Is(err, chains.ErrFork):
		return chains.ErrFork.Error()
	case errors.Is(err, errSlowGap):
		return errSlowGap.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "request timed out"
	case errors.As(err, &httpErr):
		return fmt.Sprintf("HTTP %d", httpErr.StatusCode)
	case errors.Is(err, chains.ErrRateLimited):
		return "HTTP 429"
	case errors.As(err, &rpcErr):
		return fmt.Sprintf("JSON-RPC %d", rpcErr.ErrorCode())
	case errors.As(err, &networkError):
		if networkError.Timeout() {
			return "network timeout"
		}
		return "network connection failed"
	}
	if st, ok := status.FromError(err); ok {
		return "gRPC " + st.Code().String()
	}
	return "provider request or response validation failed"
}
func (n *endpoint) label() string {
	u, _ := url.Parse(n.cfg.URL)
	protocol, _ := chains.EndpointProtocol(n.cfg)
	if u == nil || u.Hostname() == "" {
		return protocol
	}
	return protocol + " " + u.Hostname()
}
func (n *endpoint) diagnostic(err error) error {
	return fmt.Errorf("%s: %s", n.label(), failureReason(err))
}
func (p *SourcePool) unavailable(operation string, cause error) error {
	var next *endpoint
	now := p.now()
	for _, n := range p.nodes {
		if n.retry.After(now) && (next == nil || n.retry.Before(next.retry)) {
			next = n
		}
	}
	msg := fmt.Sprintf("%s %s unavailable", p.network, operation)
	if cause != nil {
		msg += ": " + cause.Error()
	}
	if next != nil {
		msg += fmt.Sprintf("; %s cooling down for %s (%s)", next.label(), next.retry.Sub(now).Round(time.Second), next.lastFailure)
	}
	return errors.New(msg)
}
