package admin

import (
	"net/http"
	"strings"

	"github.com/GMWalletApp/epusdt/internal/diagnostics"
	"github.com/labstack/echo/v4"
)

// ExportDiagnostics downloads support events from the rolling 24-hour window.
// @Summary Download detailed diagnostic events (JSON Lines)
// @Tags Admin Settings
// @Produce application/x-ndjson
// @Security AdminJWT
// @Param reference query string false "Exact gateway trade ID, merchant order ID or transaction hash"
// @Success 200 {file} file
// @Router /admin/api/v1/diagnostics/export [get]
func (c *BaseAdminController) ExportDiagnostics(ctx echo.Context) error {
	ref := strings.TrimSpace(ctx.QueryParam("reference"))
	if len(ref) > 256 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid reference")
	}
	out := ctx.Response()
	out.Header().Set("Cache-Control", "no-store")
	out.Header().Set("X-Content-Type-Options", "nosniff")
	out.Header().Set("Content-Type", "application/x-ndjson")
	out.Header().Set("Content-Disposition", `attachment; filename="epusdt-diagnostics.jsonl"`)
	if err := diagnostics.Export(ctx.Request().Context(), out, ref); err != nil {
		if out.Committed {
			return err
		}
		out.Header().Del("Content-Disposition")
		return echo.NewHTTPError(http.StatusServiceUnavailable, "diagnostics unavailable")
	}
	return nil
}
