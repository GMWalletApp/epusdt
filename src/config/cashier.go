package config

import (
	"net/url"
	"os"
	"strings"
)

// CashierScriptOrigin is deployment-owned so a brand setting cannot turn the
// administration host into a native script host. The installer reads it too.
func CashierScriptOrigin() *url.URL {
	u, err := url.Parse(strings.TrimSpace(os.Getenv("CASHIER_SCRIPT_ORIGIN")))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.Contains(u.Host, "*") {
		return nil
	}
	u.Host = strings.ToLower(u.Host)
	if u.Port() == "443" {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	u.Path = ""
	return u
}
