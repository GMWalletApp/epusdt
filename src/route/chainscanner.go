package route

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/labstack/echo/v4"
)

// Only the enumerated routes can receive either upstream token. The browser
// presents its EPUSDT admin JWT and never needs a ChainScanner credential.
func registerScannerAdmin(group *echo.Group) {
	scanner := group.Group("/chainscanner")
	for _, r := range []struct{ method, path string }{
		{"GET", "/admin/blacklist"}, {"PUT", "/admin/blacklist/:host"}, {"DELETE", "/admin/blacklist/:host"},
		{"GET", "/admin/progress"},
		{"GET", "/admin/nodes"}, {"POST", "/admin/nodes"}, {"POST", "/admin/nodes/fallback"}, {"DELETE", "/admin/nodes/:id"},
		{"POST", "/admin/nodes/:id/test"}, {"POST", "/admin/nodes/test"},
		{"GET", "/heights/:network"}, {"GET", "/transactions/:network/:hash"}, {"GET", "/transactions/:network/:hash/strict"},
	} {
		scanner.Add(r.method, r.path, scannerForward(r.path))
	}
}

var scannerHTTP = &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func scannerForward(template string) echo.HandlerFunc {
	return func(c echo.Context) error {
		base, err := config.ScannerEndpoint()
		if err != nil {
			return echo.NewHTTPError(503, err.Error())
		}
		token, err := config.ScannerToken(strings.HasPrefix(template, "/admin/"))
		if err != nil {
			return echo.NewHTTPError(503, err.Error())
		}
		parts := strings.Split(template, "/")
		for i, p := range parts {
			if strings.HasPrefix(p, ":") {
				v := c.Param(p[1:])
				if v == "" || v == "." || v == ".." || strings.ContainsAny(v, "/%\\?#") {
					return echo.NewHTTPError(400, "invalid scanner path parameter")
				}
				parts[i] = url.PathEscape(v)
			}
		}
		target := base + "/v1" + strings.Join(parts, "/")
		if q := c.QueryParams().Encode(); q != "" {
			target += "?" + q
		}
		in := c.Request()
		req, err := http.NewRequestWithContext(in.Context(), in.Method, target, http.MaxBytesReader(c.Response(), in.Body, 8192))
		if err != nil {
			return echo.NewHTTPError(400, "invalid scanner request")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if strings.HasSuffix(template, "/strict") {
			req.Header.Set("X-ChainScanner-Exclude", in.Header.Get("X-ChainScanner-Exclude"))
		}
		res, err := scannerHTTP.Do(req)
		if err != nil {
			return echo.NewHTTPError(502, "ChainScanner unavailable")
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized {
			return echo.NewHTTPError(http.StatusBadGateway, "ChainScanner rejected its configured credential")
		}
		for _, key := range []string{"Content-Type", "X-ChainScanner-Strict-Rounds", "X-ChainScanner-Manual-Review", "X-ChainScanner-Pair", "Retry-After"} {
			if v := res.Header.Get(key); v != "" {
				c.Response().Header().Set(key, v)
			}
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		c.Response().WriteHeader(res.StatusCode)
		_, err = io.Copy(c.Response(), res.Body)
		return err
	}
}
