package config

import (
	"errors"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/viper"
)

func scannerOption(key string) string {
	if s, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(viper.GetString(strings.ToLower(key)))
}

// ScannerConnection is resolved once at startup and shared by the payment
// workers and the authenticated admin proxy.
type ScannerConnection struct {
	URL, ConsumerToken, AdminToken, Transport string
	Embedded                                  bool
}

func ExternalScanner() ScannerConnection {
	return ScannerConnection{
		URL: scannerOption("CHAINSCANNER_URL"), ConsumerToken: scannerOption("CHAINSCANNER_TOKEN"),
		AdminToken: scannerOption("CHAINSCANNER_ADMIN_TOKEN"), Transport: scannerOption("CHAINSCANNER_TRANSPORT"),
	}
}

func (c ScannerConnection) Endpoint() (string, error) {
	raw := c.URL
	if raw == "" {
		return "", errors.New("embedded ChainScanner unavailable; check EPUSDT startup logs")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("configure CHAINSCANNER_URL with the scanner HTTP(S) origin")
	}
	return strings.TrimRight(raw, "/"), nil
}

func (c ScannerConnection) Token(admin bool) (string, error) {
	token := c.ConsumerToken
	if admin {
		other := c.AdminToken
		if other == token {
			return "", errors.New("configure a separate ChainScanner administrator token")
		}
		token = other
	}
	if len(token) < 24 {
		return "", errors.New("ChainScanner token missing or too short")
	}
	return token, nil
}
