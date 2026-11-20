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
	e.URL = canonicalNodeURL(e.URL)
	if chains.IsEVM(e.Network) && e.Protocol == "jsonrpc" {
		e.Protocol = "http"
	}
	return e
}
