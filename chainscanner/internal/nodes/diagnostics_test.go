package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
	"github.com/ethereum/go-ethereum/rpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUnavailabilityPreservesCodesAndOmitsCredentials(t *testing.T) {
	ctx := context.Background()
	p := NewSourcePool(ctx, "aptos")
	cfg := chains.Network{Name: "aptos", URL: "https://provider.test/secret-path?key=secret-query", APIKey: "secret-key"}
	if err := p.SetNodes([]chains.Network{cfg}); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	now := time.Now()
	p.now = func() time.Time { return now }
	p.open = func(context.Context, chains.Network) (chains.Source, error) {
		return nil, rpc.HTTPError{StatusCode: 429, Status: "secret-message", Body: []byte("secret-body")}
	}
	for range 3 {
		_, err := p.Head(ctx)
		if err == nil || !strings.Contains(err.Error(), "http provider.test: HTTP 429") || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
	now = now.Add(10 * time.Second)
	_, err := p.Head(ctx)
	if err == nil || !strings.Contains(err.Error(), "50s (HTTP 429)") || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{chains.ErrRateLimited, "HTTP 429"}, {status.Error(codes.ResourceExhausted, "secret-body"), "gRPC ResourceExhausted"},
		{fmt.Errorf("secret: %w", context.DeadlineExceeded), "request timed out"}, {errors.New("secret"), "provider request or response validation failed"},
	} {
		if got := failureReason(tc.err); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
