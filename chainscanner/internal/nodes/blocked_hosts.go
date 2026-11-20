package nodes

import (
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

type blockedHosts map[string]bool

var hostnameLabel = regexp.MustCompile("^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")

var errBlacklistHost = errors.New("blacklist expects a domain or IP literal without scheme, wildcard, port or path")

func normalizeHost(entry string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(entry)), ".")
	if ip, err := netip.ParseAddr(host); err == nil && ip.Zone() == "" {
		return ip.Unmap().String(), nil
	}
	for _, label := range strings.Split(host, ".") {
		if len(host) > 253 || !hostnameLabel.MatchString(label) {
			return "", errBlacklistHost
		}
	}
	return host, nil
}

func parseBlockedHosts(entries []string) (blockedHosts, error) {
	hosts := make(blockedHosts, len(entries))
	for _, entry := range entries {
		host, err := normalizeHost(entry)
		if err != nil {
			return nil, err
		}
		hosts[host] = true
	}
	return hosts, nil
}

// Match URL hosts at label boundaries. Ports, paths and credentials do not
// change the host. IP literals match only their normalized address.
func (hosts blockedHosts) matches(raw string) bool {
	if len(hosts) == 0 || raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		return hosts[ip.Unmap().String()]
	}
	for host != "" {
		if hosts[host] {
			return true
		}
		_, host, _ = strings.Cut(host, ".")
	}
	return false
}
