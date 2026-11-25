package route

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/scanneradmin"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/labstack/echo/v4"
)

// Only the enumerated routes can receive either upstream token. The browser
// presents its EPUSDT admin JWT and never needs a ChainScanner credential.
func registerScannerAdmin(group *echo.Group, connection config.ScannerConnection) {
	scanner := group.Group("/chainscanner")
	// @Summary Get scanner connection and supported operations
	// @Tags ChainScanner
	// @Security AdminJWT
	// @Produce json
	// @Success 200 {object} scanneradmin.ConnectionInfo
	// @Router /admin/api/v1/chainscanner/admin/connection [get]
	scanner.GET("/admin/connection", func(c echo.Context) error {
		info, err := scanneradmin.Connection(c.Request().Context(), connection)
		if err != nil {
			return echo.NewHTTPError(502, err.Error())
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(200, info)
	})
	// @Summary Preview legacy node migration
	// @Tags ChainScanner
	// @Security AdminJWT
	// @Produce json
	// @Success 200 {object} data.NodeMigration
	// @Router /admin/api/v1/chainscanner/admin/migration [get]
	scanner.GET("/admin/migration", func(c echo.Context) error {
		report, err := data.MigrateScannerNodes(c.Request().Context(), connection, false, nil)
		if err != nil {
			return echo.NewHTTPError(502, err.Error())
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(200, report)
	})
	// @Summary Apply migration to the confirmed scanner
	// @Tags ChainScanner
	// @Security AdminJWT
	// @Accept json
	// @Produce json
	// @Param body body data.MigrationTarget true "Target returned by the preview"
	// @Success 200 {object} data.NodeMigration
	// @Router /admin/api/v1/chainscanner/admin/migration [post]
	scanner.POST("/admin/migration", func(c echo.Context) error {
		var confirm data.MigrationTarget
		if c.Bind(&confirm) != nil || confirm.StoreID == "" || confirm.Target == "" {
			return echo.NewHTTPError(400, "confirm the current ChainScanner target")
		}
		report, err := data.MigrateScannerNodes(c.Request().Context(), connection, true, &confirm)
		if err != nil {
			return echo.NewHTTPError(409, err.Error())
		}
		return c.JSON(200, report)
	})
	// @Summary Stream scanner progress by network
	// @Description The progress event contains the same JSON array as GET /admin/progress. EPUSDT reads the configured scanner on connection and every five seconds. A scanner_error event indicates a temporarily unavailable snapshot. The stream stays open for recovery.
	// @Tags ChainScanner
	// @Security AdminJWT
	// @Produce text/event-stream
	// @Success 200 {string} string "Server-sent events with progress snapshots or scanner errors"
	// @Router /admin/api/v1/chainscanner/admin/progress/stream [get]
	scanner.GET("/admin/progress/stream", scannerProgressStream(connection, 5*time.Second))
	for _, r := range []struct{ method, path string }{
		// @Summary List blocked node hosts
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Produce json
		// @Success 200 {array} object{host=string,source=string}
		// @Router /admin/api/v1/chainscanner/admin/blacklist [get]
		{"GET", "/admin/blacklist"},

		// @Summary Block a node host
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Param host path string true "Hostname"
		// @Success 204
		// @Router /admin/api/v1/chainscanner/admin/blacklist/{host} [put]
		{"PUT", "/admin/blacklist/:host"},

		// @Summary Remove an administrator host block
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Param host path string true "Hostname"
		// @Success 204
		// @Router /admin/api/v1/chainscanner/admin/blacklist/{host} [delete]
		{"DELETE", "/admin/blacklist/:host"},

		// @Summary Get scanner progress by network
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Produce json
		// @Success 200 {array} engine.ScanProgress
		// @Router /admin/api/v1/chainscanner/admin/progress [get]
		{"GET", "/admin/progress"},

		// @Summary Get block scan retry rates by network
		// @Description Counts provider range reads since scanner startup, including failover and gap retries. retry_rate is retry_attempts divided by total_attempts, or null before the first read.
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Produce json
		// @Param network query string false "Limit to one configured network"
		// @Success 200 {array} scanneradmin.ScanRetryStats
		// @Failure 400 {string} string "Invalid network filter"
		// @Failure 404 {string} string "Scanner version does not expose retry rates"
		// @Router /admin/api/v1/chainscanner/admin/scan-retry-rate [get]
		{"GET", "/admin/scan-retry-rate"},

		// @Summary List scanner nodes
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Produce json
		// @Param network query string false "Limit to one network"
		// @Success 200 {array} nodes.Node
		// @Router /admin/api/v1/chainscanner/admin/nodes [get]
		{"GET", "/admin/nodes"},

		// @Summary Add a regular node
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Accept json
		// @Produce json
		// @Param body body nodes.NodeOptions true "Node configuration"
		// @Success 201 {object} nodes.Node
		// @Router /admin/api/v1/chainscanner/admin/nodes [post]
		{"POST", "/admin/nodes"},

		// @Summary Add a fallback node
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Accept json
		// @Produce json
		// @Param body body nodes.NodeOptions true "Fallback node configuration"
		// @Success 201 {object} nodes.Node
		// @Router /admin/api/v1/chainscanner/admin/nodes/fallback [post]
		{"POST", "/admin/nodes/fallback"},

		// @Summary Update node settings or activation
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Accept json
		// @Produce json
		// @Param id path int true "Node ID"
		// @Param body body nodes.NodePatch true "Changed fields"
		// @Success 200 {object} nodes.Node
		// @Router /admin/api/v1/chainscanner/admin/nodes/{id} [patch]
		{"PATCH", "/admin/nodes/:id"},

		// @Summary Remove a node
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Param id path int true "Node ID"
		// @Success 204
		// @Router /admin/api/v1/chainscanner/admin/nodes/{id} [delete]
		{"DELETE", "/admin/nodes/:id"},

		// @Summary Check one enabled node
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Produce json
		// @Param id path int true "Node ID"
		// @Success 200 {object} nodes.Node
		// @Router /admin/api/v1/chainscanner/admin/nodes/{id}/test [post]
		{"POST", "/admin/nodes/:id/test"},

		// @Summary Check enabled nodes
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Produce json
		// @Param network query string false "Limit to one network"
		// @Param include_manual query bool false "Also check manual verification nodes"
		// @Success 200 {array} nodes.Node
		// @Router /admin/api/v1/chainscanner/admin/nodes/test [post]
		{"POST", "/admin/nodes/test"},

		// @Summary Get the scanner height
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Produce json
		// @Param network path string true "Network"
		// @Success 200 {integer} int64
		// @Router /admin/api/v1/chainscanner/heights/{network} [get]
		{"GET", "/heights/:network"},

		// @Summary Look up a transaction
		// @Tags ChainScanner
		// @Security AdminJWT
		// @Produce json
		// @Param network path string true "Network"
		// @Param hash path string true "Transaction hash"
		// @Param manual query bool false "Allow manual verification nodes"
		// @Param fallback_only query bool false "Restrict lookup to fallback nodes"
		// @Success 200 {object} object{network=string,hash=string,result=object}
		// @Router /admin/api/v1/chainscanner/transactions/{network}/{hash} [get]
		{"GET", "/transactions/:network/:hash"},
	} {
		scanner.Add(r.method, r.path, scannerForward(r.path, connection))
	}
}

var scannerHTTP = &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func scannerForward(template string, connection config.ScannerConnection) echo.HandlerFunc {
	return func(c echo.Context) error {
		base, err := connection.Endpoint()
		if err != nil {
			return echo.NewHTTPError(503, err.Error())
		}
		token, err := connection.Token(strings.HasPrefix(template, "/admin/"))
		if err != nil {
			return echo.NewHTTPError(503, err.Error())
		}
		parts := strings.Split(template, "/")
		for i, p := range parts {
			if strings.HasPrefix(p, ":") {
				v := c.Param(p[1:])
				if p == ":host" {
					// Browser clients encode the colon in exact: and keyword: rules.
					v, err = url.PathUnescape(v)
					if err != nil {
						return echo.NewHTTPError(400, "invalid scanner host rule")
					}
				}
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
		client := scannerHTTP
		if template == "/admin/nodes/test" {
			// A large inventory can exceed the per-request timeout. Cancellation
			// follows the administrator connection, and every probe has its own limit.
			client = &http.Client{CheckRedirect: scannerHTTP.CheckRedirect}
		}
		res, err := client.Do(req)
		if err != nil {
			return echo.NewHTTPError(502, "ChainScanner unavailable")
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized {
			return echo.NewHTTPError(http.StatusBadGateway, "ChainScanner rejected its configured credential")
		}
		for _, key := range []string{"Content-Type", "X-ChainScanner-Evidence", "X-ChainScanner-Scan-Source", "X-ChainScanner-Height-Checked", "Retry-After"} {
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

func scannerProgressStream(connection config.ScannerConnection, interval time.Duration) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		out := c.Response()
		out.Header().Set(echo.HeaderContentType, "text/event-stream; charset=utf-8")
		out.Header().Set("Cache-Control", "no-store")
		out.Header().Set("X-Accel-Buffering", "no")
		out.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(out.Writer)
		if _, err := fmt.Fprint(out, "retry: 5000\n\n"); err != nil {
			return err
		}
		if err := controller.Flush(); err != nil {
			return err
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			progress, err := scanneradmin.Progress(ctx, connection)
			if ctx.Err() != nil {
				return nil
			}
			if err == nil {
				_, err = fmt.Fprintf(out, "event: progress\ndata: %s\n\n", progress)
			} else {
				_, err = fmt.Fprint(out, "event: scanner_error\ndata: {}\n\n")
			}
			if err != nil {
				return err
			}
			if err := controller.Flush(); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
}
