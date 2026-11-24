package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const dnsLookupTimeout = 3 * time.Second

var specialRoutes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("168.63.129.16/32"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

var globalIPv6 = netip.MustParsePrefix("2000::/3")

var lookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

var dialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext

func DialPublicContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	lookup, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()
	addrs, err := lookupIPAddr(lookup, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, errors.New("callback host has no addresses")
	}
	for _, addr := range addrs {
		if addr.Zone != "" {
			return nil, errors.New("callback address has an IP zone")
		}
		if err := validatePublicIP(host, addr.IP); err != nil {
			return nil, err
		}
	}
	for _, addr := range addrs {
		conn, dialErr := dialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
	}
	return nil, err
}

// ValidatePublicHTTPURL verifies that raw is an HTTP(S) URL whose host does not
// point to localhost, private networks, link-local addresses, or metadata IPs.
func ValidatePublicHTTPURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("url is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("url scheme must be http or https")
	}

	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return errors.New("url host is required")
	}
	if strings.Contains(host, "%") {
		return fmt.Errorf("url host %q is not allowed", host)
	}
	if strings.TrimSuffix(strings.ToLower(host), ".") == "localhost" {
		return fmt.Errorf("url host %q is not allowed", host)
	}

	if ip := net.ParseIP(host); ip != nil {
		return validatePublicIP(host, ip)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dnsLookupTimeout)
	defer cancel()

	addrs, err := lookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve url host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("resolve url host %q: no addresses", host)
	}
	for _, addr := range addrs {
		if err := validatePublicIP(host, addr.IP); err != nil {
			return err
		}
	}
	return nil
}

func validatePublicIP(host string, ip net.IP) error {
	if isUnsafeIP(ip) {
		return fmt.Errorf("url host %q resolves to disallowed address %s", host, ip.String())
	}
	return nil
}

func isUnsafeIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.Is6() && !globalIPv6.Contains(addr) {
		return true
	}
	for _, route := range specialRoutes {
		if route.Contains(addr) {
			return true
		}
	}
	return false
}
