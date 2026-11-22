package http

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/labstack/echo/v4"
)

func cashierHostname(host string) string {
	return strings.TrimSuffix(strings.ToLower((&url.URL{Host: host}).Hostname()), ".")
}

// Only public payment routes and their static files belong on the native
// widget hostname. All ports on a hostname share the browser's host cookies.
func PublicCashierBoundary(origin *url.URL, installing bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			p := path.Clean(req.URL.Path)
			requestOrigin := req.Header.Get("Origin")
			source, _ := url.Parse(requestOrigin)
			fromCashier := origin != nil && source != nil && cashierHostname(source.Host) == cashierHostname(origin.Host)
			onCashier := origin != nil && cashierHostname(req.Host) == cashierHostname(origin.Host)
			if onCashier && (installing || !publicCashierPath(p)) {
				return c.NoContent(http.StatusNotFound)
			}
			if (fromCashier || requestOrigin == "null") && (installing || strings.HasPrefix(p, "/admin/") || strings.HasPrefix(p, "/api/")) {
				return c.NoContent(http.StatusForbidden)
			}
			return next(c)
		}
	}
}

func publicCashierPath(p string) bool {
	for _, prefix := range []string{"/cashier/", "/payments/", "/pay/", "/assets/", "/images/", "/static/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	// The embedded web build also puts PWA assets and icons at the root.
	return path.Dir(p) == "/" && path.Ext(p) != "" && path.Ext(p) != ".html"
}
