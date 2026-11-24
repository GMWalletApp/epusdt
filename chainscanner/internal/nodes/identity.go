package nodes

import (
	"net"
	"net/url"
	"strings"

	"github.com/GMWalletApp/ChainScanner/internal/chains"
)

// Normalize only equivalent URL spellings. Paths, escaping, and queries may
// select a different endpoint or credential and are significant.
func canonicalNodeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if ((u.Scheme == "https" || u.Scheme == "wss") && port == "443") || ((u.Scheme == "http" || u.Scheme == "ws") && port == "80") {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	if u.EscapedPath() == "/" {
		u.Path = ""
		u.RawPath = ""
	}
	return u.String()
}

func (e NodeEndpoint) identity() NodeEndpoint {
	e.URL = strings.TrimSpace(e.URL)
	if p, err := chains.EndpointProtocol(chains.Network{Name: e.Network, Protocol: e.Protocol, URL: e.URL}); err == nil {
		e.Protocol = p
	}
	if e.Protocol == "grpc" || e.Protocol == "solidity-grpc" {
		if host, ok := strings.CutPrefix(e.URL, "grpcs://"); ok {
			e.URL = "https://" + host
		} else if host, ok := strings.CutPrefix(e.URL, "grpc://"); ok {
			e.URL = "http://" + host
		}
	}
	e.URL = canonicalNodeURL(e.URL)
	if e.Network == "aptos" && (e.Protocol == "http" || e.Protocol == "rest") {
		u, err := url.Parse(e.URL)
		if err == nil && strings.HasSuffix(u.EscapedPath(), "/v1/") {
			u.Path = strings.TrimSuffix(u.Path, "/")
			u.RawPath = strings.TrimSuffix(u.RawPath, "/")
			e.URL = u.String()
		}
	}
	if e.Network == "bitcoin" && e.Protocol == "http" {
		e.Protocol = "jsonrpc"
	}
	if (chains.IsEVM(e.Network) || e.Network == "solana") && e.Protocol == "jsonrpc" ||
		(e.Network == "aptos" || e.Network == "tron") && e.Protocol == "rest" {
		e.Protocol = "http"
	}
	return e
}

// PublicNode advertises its execution HTTP and WS interfaces on the same
// hostname. Reserve one history lane per gateway across both transports.
func historyLane(network, raw string) string {
	canonical := canonicalNodeURL(raw)
	u, err := url.Parse(canonical)
	if err == nil && chains.IsEVM(network) && u.Scheme == "wss" && strings.HasSuffix(u.Hostname(), ".publicnode.com") {
		u.Scheme = "https"
		return u.String()
	}
	return canonical
}
