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

// ScannerEndpoint is set by the operator, never by an incoming API request.
func ScannerEndpoint() (string, error) {
	raw := scannerOption("CHAINSCANNER_URL")
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("configure CHAINSCANNER_URL with the scanner HTTP(S) origin")
	}
	return strings.TrimRight(raw, "/"), nil
}

func ScannerToken(admin bool) (string, error) {
	token := scannerOption("CHAINSCANNER_TOKEN")
	if admin {
		other := scannerOption("CHAINSCANNER_ADMIN_TOKEN")
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

func ScannerTransport() string { return scannerOption("CHAINSCANNER_TRANSPORT") }
