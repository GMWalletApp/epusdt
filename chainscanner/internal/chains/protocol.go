package chains

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

// Protocols lists selectable node interfaces in preferred order. HTTP is
// retained for configurations created before explicit protocol selection.
func Protocols(network string) []string {
	switch {
	case network == "bitcoin":
		return []string{"jsonrpc", "http"}
	case network == "ethereum":
		return []string{"ws", "http", "jsonrpc", "graphql", "ipc", "sse"}
	case IsEVM(network):
		return []string{"ws", "http", "jsonrpc", "graphql", "ipc"}
	case network == "tron":
		return []string{"solidity-grpc", "solidity-http", "grpc", "http", "rest", "jsonrpc"}
	case network == "sui":
		return []string{"grpc", "http", "graphql"}
	case network == "aptos":
		return []string{"grpc", "http", "rest", "graphql"}
	case network == "solana":
		return []string{"ws", "grpc", "http", "jsonrpc"}
	case network == "ton":
		return []string{"lite", "rest", "jsonrpc"}
	}
	return nil
}

func NeedsHistory(network, protocol string) bool {
	switch network {
	case "solana":
		return protocol == "ws" || protocol == "grpc"
	case "aptos":
		return protocol == "grpc" || protocol == "graphql"
	case "ethereum":
		return protocol == "sse"
	case "tron":
		return protocol == "jsonrpc"
	default:
		return false
	}
}

// Avalanche WebSockets may either serve execution RPC directly or supply
// only newHeads, with an HTTP companion performing all execution reads.
func (n Network) UsesHistory(protocol string) bool {
	return NeedsHistory(n.Name, protocol) || (n.Name == "avalanche" && protocol == "ws" && n.HistoryURL != "")
}

func (n Network) endpoint() (*url.URL, string, error) {
	u, err := url.Parse(n.URL)
	if err != nil || u.User != nil || u.Fragment != "" {
		return nil, "", errors.New("invalid node URL")
	}
	p := n.Protocol
	if p == "" {
		switch {
		case u.Scheme == "grpc" || u.Scheme == "grpcs":
			p = "grpc"
		case u.Scheme == "ws" || u.Scheme == "wss":
			p = "ws"
		case u.Scheme == "ipc":
			p = "ipc"
		case n.Name == "ton":
			p = "lite"
		default:
			p = "http"
		}
	}
	if !slices.Contains(Protocols(n.Name), p) {
		return nil, "", fmt.Errorf("unsupported %q protocol for %s", p, n.Name)
	}
	if n.Name == "sui" && p == "http" {
		p = "grpc"
	}
	httpURL := u.Scheme == "http" || u.Scheme == "https"
	valid := httpURL
	switch p {
	case "lite":
		if u.Scheme == "adnl" {
			_, err := tonPeerKey(u)
			valid = err == nil
		}
	case "grpc", "solidity-grpc":
		valid = (httpURL || u.Scheme == "grpc" || u.Scheme == "grpcs") && (u.Path == "" || u.Path == "/") && u.RawQuery == ""
	case "ipc":
		valid = u.Scheme == "ipc" && u.Host == "" && filepath.IsAbs(u.Path) && u.RawQuery == ""
	case "ws":
		valid = u.Scheme == "ws" || u.Scheme == "wss"
	}
	if !valid || (p != "ipc" && u.Hostname() == "") {
		return nil, "", fmt.Errorf("invalid %s endpoint for %s", p, n.Name)
	}
	if n.TraceCalls && p == "graphql" && IsEVM(n.Name) {
		return nil, "", errors.New("call tracing requires execution JSON-RPC")
	}
	return u, p, nil
}

// ValidateEndpoint checks a primary endpoint before a history node is selected.
func ValidateEndpoint(n Network) error { _, _, err := n.endpoint(); return err }

func ValidateNetwork(n Network) error {
	if err := n.ValidateAssetEvents(); err != nil {
		return err
	}
	_, p, err := n.endpoint()
	if err != nil {
		return err
	}
	if n.UsesHistory(p) {
		u, e := url.Parse(n.HistoryURL)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
			return errors.New("a separate HTTP history_url is required for this protocol")
		}
	} else if n.HistoryURL != "" || n.HistoryAPIKey != "" {
		return errors.New("history endpoint is unused by this protocol")
	}
	return nil
}

func redactEndpointError(s string, n Network) string {
	for _, v := range []string{n.URL, n.HistoryURL, n.APIKey, n.HistoryAPIKey} {
		if v != "" {
			s = strings.ReplaceAll(s, v, "<redacted>")
		}
	}
	return s
}

// EndpointProtocol resolves the transport, including legacy configured URLs.
func EndpointProtocol(n Network) (string, error) { _, p, err := n.endpoint(); return p, err }
