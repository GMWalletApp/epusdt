package nodes

import (
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// rules uses the same spelling as the API and SQLite. Build the keyword slice
// once on edits so domain-only installations keep their label lookup cost.
type blockedHosts struct {
	rules    map[string]bool
	keywords []string
}

func compileBlacklist(rules map[string]bool) blockedHosts {
	compiled := blockedHosts{rules: rules}
	for rule := range rules {
		if keyword, ok := strings.CutPrefix(rule, "keyword:"); ok {
			compiled.keywords = append(compiled.keywords, keyword)
		}
	}
	return compiled
}

var hostnameLabel = regexp.MustCompile("^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")

var keywordPattern = regexp.MustCompile("^[a-z0-9._-]{1,253}$")
var errBlacklistHost = errors.New("expected a domain, IP, exact:host or keyword:text")

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

func normalizeRule(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if word, ok := strings.CutPrefix(raw, "keyword:"); ok {
		if !keywordPattern.MatchString(word) {
			return "", errBlacklistHost
		}
		return "keyword:" + word, nil
	}
	if value, ok := strings.CutPrefix(raw, "exact:"); ok {
		host, err := normalizeHost(value)
		if err != nil {
			return "", err
		}
		return "exact:" + host, nil
	}
	return normalizeHost(raw)
}

func parseBlockedHosts(entries []string) (blockedHosts, error) {
	rules := make(map[string]bool, len(entries))
	for _, entry := range entries {
		host, err := normalizeRule(entry)
		if err != nil {
			return blockedHosts{}, err
		}
		rules[host] = true
	}
	return compileBlacklist(rules), nil
}

// Match only the URL hostname. Unprefixed domains include their subdomains.
func (hosts blockedHosts) matches(raw string) bool {
	if len(hosts.rules) == 0 || raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	ip, ipErr := netip.ParseAddr(host)
	if ipErr == nil {
		host = ip.Unmap().String()
	}
	if hosts.rules["exact:"+host] {
		return true
	}
	for _, word := range hosts.keywords {
		if strings.Contains(host, word) {
			return true
		}
	}
	if ipErr == nil {
		return hosts.rules[host]
	}
	for host != "" {
		if hosts.rules[host] {
			return true
		}
		_, host, _ = strings.Cut(host, ".")
	}
	return false
}
