package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/labstack/echo/v4"
)

// OverviewResponse is the dashboard top-card response.
type OverviewResponse struct {
	TotalAsset      float64 `json:"total_asset" example:"12345.6789"`
	Volume          float64 `json:"volume" example:"100.5"`
	OrderCount      int64   `json:"order_count" example:"42"`
	SuccessRate     float64 `json:"success_rate" example:"0.85"`
	SevenDayVolume  float64 `json:"seven_day_volume" example:"700.25"`
	ActiveAddresses int64   `json:"active_addresses" example:"5"`
	OnlineChains    int64   `json:"online_chains" example:"3"`
}

// OrderStatsResponse is the order statistics response.
type OrderStatsResponse struct {
	OrderCount         int64   `json:"order_count" example:"100"`
	SuccessCount       int64   `json:"success_count" example:"85"`
	SuccessRate        float64 `json:"success_rate" example:"0.85"`
	AvgPaymentSeconds  float64 `json:"avg_payment_seconds" example:"120.5"`
	ExpiredUnpaidCount int64   `json:"expired_unpaid_count" example:"10"`
}

// RpcStatsDashboardResponse is the RPC runtime stats payload.
type RpcStatsDashboardResponse struct {
	GeneratedAt string                  `json:"generated_at" example:"2026-06-09T12:00:00+08:00"`
	Summary     RpcStatsSummaryResponse `json:"summary"`
	Chains      []RpcStatsChainResponse `json:"chains"`
}

// RpcStatsSummaryResponse is the overall RPC runtime summary.
type RpcStatsSummaryResponse struct {
	TotalChains    int     `json:"total_chains" example:"6"`
	EnabledChains  int     `json:"enabled_chains" example:"5"`
	ActiveChains   int     `json:"active_chains" example:"4"`
	OkChains       int     `json:"ok_chains" example:"3"`
	WarningChains  int     `json:"warning_chains" example:"1"`
	DownChains     int     `json:"down_chains" example:"0"`
	UnknownChains  int     `json:"unknown_chains" example:"1"`
	DisabledChains int     `json:"disabled_chains" example:"1"`
	SuccessCount   int64   `json:"success_count" example:"123"`
	FailureCount   int64   `json:"failure_count" example:"2"`
	SuccessRate    float64 `json:"success_rate" example:"0.984"`
}

// RpcStatsChainResponse is one chain's in-process RPC runtime stats.
type RpcStatsChainResponse struct {
	Network           string  `json:"network" example:"tron"`
	DisplayName       string  `json:"display_name" example:"TRON"`
	Enabled           bool    `json:"enabled" example:"true"`
	HasData           bool    `json:"has_data" example:"true"`
	Status            string  `json:"status" example:"ok" enums:"ok,warning,down,unknown,disabled"`
	SuccessCount      int64   `json:"success_count" example:"123"`
	FailureCount      int64   `json:"failure_count" example:"2"`
	SuccessRate       float64 `json:"success_rate" example:"0.984"`
	LatestBlockHeight int64   `json:"latest_block_height" example:"64532100"`
	LastSyncAt        string  `json:"last_sync_at" example:"2026-06-09T12:00:00+08:00"`
}

type rpcStatsStreamEnvelope struct {
	StatusCode int                       `json:"status_code"`
	Message    string                    `json:"message"`
	Data       RpcStatsDashboardResponse `json:"data"`
	RequestID  string                    `json:"request_id"`
}

// RpcHealthCheckResponse is the response for a health check probe.
type RpcHealthCheckResponse struct {
	// 健康状态 unknown=未知 ok=正常 down=异常
	Status        string `json:"status" example:"ok" enums:"unknown,ok,down"`
	LastLatencyMs int    `json:"last_latency_ms" example:"120"`
}

// ApiKeyStatsResponse is the response for API key usage stats.
type ApiKeyStatsResponse struct {
	CallCount  int64  `json:"call_count" example:"342"`
	LastUsedAt string `json:"last_used_at" example:"2026-04-14T08:30:00+00:00"`
}

// RotateSecretResponse is returned when rotating an API key secret.
type RotateSecretResponse struct {
	SecretKey string `json:"secret_key" example:"newSecret123abc456"`
}

// BatchImportResult is one row in the batch import response.
type BatchImportResult struct {
	Address   string `json:"address" example:"TTestTronAddress001"`
	OK        bool   `json:"ok" example:"true"`
	ErrorCode int    `json:"error_code,omitempty" example:"10001"`
	Error     string `json:"error,omitempty" example:"wallet address already exists"`
}

// SettingsUpsertResult is one row in the settings upsert response.
type SettingsUpsertResult struct {
	Key       string `json:"key" example:"rate.api_url"`
	OK        bool   `json:"ok" example:"true"`
	ErrorCode int    `json:"error_code,omitempty" example:"10043"`
	Error     string `json:"error,omitempty" example:"invalid setting item"`
}

// Overview returns the top-card numbers for the dashboard landing page.
// All values are computed with on-demand SQL — no cache — per the design.
// The selected time range (range / start_at / end_at) is applied to
// volume, order_count, success_rate and active_addresses.
// seven_day_volume always covers the rolling past 7 days.
// total_asset and online_chains are time-independent.
// @Summary      Dashboard overview
// @Description  Returns top-card numbers: total asset, volume, success rate, active addresses, etc. Supports ?range= for filtering.
// @Tags         Admin Dashboard
// @Security     AdminJWT
// @Produce      json
// @Param        range    query string false "Time range: today, 7d, 30d, custom" default(today)
// @Param        start_at query int    false "Start time (Unix seconds, for custom range)"
// @Param        end_at   query int    false "End time (Unix seconds, for custom range)"
// @Success      200 {object} response.ApiResponse{data=admin.OverviewResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/dashboard/overview [get]
func (c *BaseAdminController) Overview(ctx echo.Context) error {
	start, end := parseRange(ctx)

	now := time.Now()
	sevenDaysAgo := startOfDay(now).AddDate(0, 0, -6)

	totalActual, _ := data.SumPaidActualAmount()

	orderCount, successCount, volume, _ := data.PaidStatsInRange(start, end)
	var successRate float64
	if orderCount > 0 {
		successRate = float64(successCount) / float64(orderCount)
	}

	_, _, sevenDayActual, _ := data.PaidStatsInRange(sevenDaysAgo, now)

	activeAddr, _ := data.ActiveAddressCountInRange(start, end)
	onlineChains, _ := data.CountEnabledChains()

	return c.SucJson(ctx, map[string]interface{}{
		"total_asset":      totalActual,
		"volume":           volume,
		"order_count":      orderCount,
		"success_rate":     successRate,
		"seven_day_volume": sevenDayActual,
		"active_addresses": activeAddr,
		"online_chains":    onlineChains,
	})
}

// isHourlyRange returns true when the time window spans ≤ 1 day,
// so callers can choose hourly vs daily granularity.
func isHourlyRange(start, end time.Time) bool {
	return end.Sub(start) <= 24*time.Hour
}

// AssetTrend returns per-day actual_amount for paid orders. group_by=
// "total" yields one series; "address" yields rows keyed by
// receive_address for the stacked chart.
// Granularity: hourly when range ≤ 1 day, daily otherwise.
// Missing buckets are always zero-filled.
// @Summary      Asset trend
// @Description  Returns per-period actual_amount for paid orders. Hourly for ≤1-day ranges, daily otherwise. Missing buckets are zero-filled.
// @Tags         Admin Dashboard
// @Security     AdminJWT
// @Produce      json
// @Param        range query string false "Time range: today, 7d, 30d, custom" default(today)
// @Param        start_at query int false "Start time (Unix seconds, for custom range)"
// @Param        end_at query int false "End time (Unix seconds, for custom range)"
// @Param        group_by query string false "Group by: total or address" default(total)
// @Success      200 {object} response.ApiResponse{data=[]data.DailyStat}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/dashboard/asset-trend [get]
func (c *BaseAdminController) AssetTrend(ctx echo.Context) error {
	start, end := parseRange(ctx)
	groupBy := strings.ToLower(strings.TrimSpace(ctx.QueryParam("group_by")))
	hourly := isHourlyRange(start, end)
	if groupBy == "address" {
		var rows interface{}
		var err error
		if hourly {
			rows, err = data.HourlyAssetByAddress(start, end)
		} else {
			rows, err = data.DailyAssetByAddress(start, end)
		}
		if err != nil {
			return c.FailJson(ctx, err)
		}
		return c.SucJson(ctx, rows)
	}
	var rows interface{}
	var err error
	if hourly {
		rows, err = data.HourlyOrderStats(start, end)
	} else {
		rows, err = data.DailyOrderStats(start, end)
	}
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, rows)
}

// RevenueTrend mirrors the daily stats view used for revenue charts.
// Same data shape as AssetTrend(group_by=total) but kept as its own
// endpoint so the frontend can evolve each chart independently.
// Granularity: hourly when range ≤ 1 day, daily otherwise.
// Missing buckets are always zero-filled.
// @Summary      Revenue trend
// @Description  Returns per-period revenue stats for charts. Hourly for ≤1-day ranges, daily otherwise. Missing buckets are zero-filled.
// @Tags         Admin Dashboard
// @Security     AdminJWT
// @Produce      json
// @Param        range query string false "Time range: today, 7d, 30d, custom" default(today)
// @Param        start_at query int false "Start time (Unix seconds, for custom range)"
// @Param        end_at query int false "End time (Unix seconds, for custom range)"
// @Success      200 {object} response.ApiResponse{data=[]data.DailyStat}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/dashboard/revenue-trend [get]
func (c *BaseAdminController) RevenueTrend(ctx echo.Context) error {
	start, end := parseRange(ctx)
	var rows interface{}
	var err error
	if isHourlyRange(start, end) {
		rows, err = data.HourlyOrderStats(start, end)
	} else {
		rows, err = data.DailyOrderStats(start, end)
	}
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, rows)
}

// OrderStats returns aggregate order figures for the selected window:
// total count, success rate, average payment duration (seconds), and
// expired-unpaid count.
// @Summary      Order statistics
// @Description  Returns aggregate order figures: count, success rate, avg payment time, expired count
// @Tags         Admin Dashboard
// @Security     AdminJWT
// @Produce      json
// @Param        range query string false "Time range: today, 7d, 30d, custom" default(today)
// @Param        start_at query int false "Start time (Unix seconds, for custom range)"
// @Param        end_at query int false "End time (Unix seconds, for custom range)"
// @Success      200 {object} response.ApiResponse{data=admin.OrderStatsResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/dashboard/order-stats [get]
func (c *BaseAdminController) OrderStats(ctx echo.Context) error {
	start, end := parseRange(ctx)
	total, success, _, err := data.PaidStatsInRange(start, end)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	avg, _ := data.AveragePaymentDurationSeconds(start, end)
	expired, _ := data.CountExpiredInRange(start, end)

	var rate float64
	if total > 0 {
		rate = float64(success) / float64(total)
	}
	return c.SucJson(ctx, map[string]interface{}{
		"order_count":          total,
		"success_count":        success,
		"success_rate":         rate,
		"avg_payment_seconds":  avg,
		"expired_unpaid_count": expired,
	})
}

// RpcStats returns process-local RPC runtime counters grouped by chain.
// Values reset on process restart and are not persisted to the database.
// @Summary      RPC runtime statistics
// @Description  Streams in-process RPC success/failure counts, success rate, latest block height and last sync time by chain over one authenticated SSE connection.
// @Tags         Admin Dashboard
// @Security     AdminJWT
// @Produce      text/event-stream
// @Success      200 {object} response.ApiResponse{data=admin.RpcStatsDashboardResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/dashboard/rpc-stats [get]
func (c *BaseAdminController) RpcStats(ctx echo.Context) error {
	return c.streamRpcStats(ctx)
}

func buildRpcStatsResponse() (RpcStatsDashboardResponse, error) {
	chains, err := data.ListChains()
	if err != nil {
		return RpcStatsDashboardResponse{}, err
	}
	stats := data.SnapshotRpcRuntimeStats()
	now := time.Now()
	resp := RpcStatsDashboardResponse{
		GeneratedAt: now.Format(time.RFC3339),
		Chains:      make([]RpcStatsChainResponse, 0, len(chains)),
	}
	for _, chain := range chains {
		network := strings.ToLower(strings.TrimSpace(chain.Network))
		stat := stats[network]
		successRate := rpcStatsSuccessRate(stat.SuccessCount, stat.FailureCount)
		hasData := stat.SuccessCount+stat.FailureCount > 0
		status := rpcStatsStatus(chain.Enabled, stat.SuccessCount, stat.FailureCount)
		lastSyncAt := ""
		if !stat.LastSyncAt.IsZero() {
			lastSyncAt = stat.LastSyncAt.Format(time.RFC3339)
		}

		resp.Summary.TotalChains++
		if chain.Enabled {
			resp.Summary.EnabledChains++
		}
		if hasData {
			resp.Summary.ActiveChains++
		}
		switch status {
		case "ok":
			resp.Summary.OkChains++
		case "warning":
			resp.Summary.WarningChains++
		case "down":
			resp.Summary.DownChains++
		case "disabled":
			resp.Summary.DisabledChains++
		default:
			resp.Summary.UnknownChains++
		}
		resp.Summary.SuccessCount += stat.SuccessCount
		resp.Summary.FailureCount += stat.FailureCount

		resp.Chains = append(resp.Chains, RpcStatsChainResponse{
			Network:           chain.Network,
			DisplayName:       rpcStatsDisplayName(chain.Network, chain.DisplayName),
			Enabled:           chain.Enabled,
			HasData:           hasData,
			Status:            status,
			SuccessCount:      stat.SuccessCount,
			FailureCount:      stat.FailureCount,
			SuccessRate:       successRate,
			LatestBlockHeight: stat.LatestBlockHeight,
			LastSyncAt:        lastSyncAt,
		})
	}
	resp.Summary.SuccessRate = rpcStatsSuccessRate(resp.Summary.SuccessCount, resp.Summary.FailureCount)
	return resp, nil
}

func (c *BaseAdminController) streamRpcStats(ctx echo.Context) error {
	initial, err := buildRpcStatsResponse()
	if err != nil {
		return c.FailJson(ctx, err)
	}

	resp := ctx.Response()
	resp.Header().Set(echo.HeaderContentType, "text/event-stream; charset=utf-8")
	resp.Header().Set("Cache-Control", "no-cache")
	resp.Header().Set("Connection", "keep-alive")
	resp.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := resp.Writer.(http.Flusher)
	if !ok {
		return echo.NewHTTPError(http.StatusInternalServerError, "streaming unsupported")
	}

	resp.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(resp.Writer, "retry: 5000\n\n"); err != nil {
		return err
	}
	if err := writeRpcStatsSSE(ctx, flusher, initial); err != nil {
		return err
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Request().Context().Done():
			return nil
		case <-ticker.C:
			payload, err := buildRpcStatsResponse()
			if err != nil {
				return err
			}
			if err := writeRpcStatsSSE(ctx, flusher, payload); err != nil {
				return err
			}
		}
	}
}

func writeRpcStatsSSE(ctx echo.Context, flusher http.Flusher, payload RpcStatsDashboardResponse) error {
	envelope := rpcStatsStreamEnvelope{
		StatusCode: http.StatusOK,
		Message:    "success",
		Data:       payload,
		RequestID:  ctx.Request().Header.Get(echo.HeaderXRequestID),
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(ctx.Response().Writer, "event: rpc_stats\ndata: %s\n\n", b); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func rpcStatsDisplayName(network string, displayName string) string {
	if name := strings.TrimSpace(displayName); name != "" {
		return name
	}
	return strings.TrimSpace(network)
}

func rpcStatsSuccessRate(successCount, failureCount int64) float64 {
	total := successCount + failureCount
	if total <= 0 {
		return 0
	}
	return float64(successCount) / float64(total)
}

func rpcStatsStatus(enabled bool, successCount, failureCount int64) string {
	if !enabled {
		return "disabled"
	}
	if successCount+failureCount == 0 {
		return "unknown"
	}
	if successCount == 0 {
		return "down"
	}
	if failureCount > 0 {
		return "warning"
	}
	return "ok"
}

// RecentOrders returns the last N orders for the dashboard list card.
// @Summary      Recent orders
// @Description  Returns the last N orders for the dashboard
// @Tags         Admin Dashboard
// @Security     AdminJWT
// @Produce      json
// @Param        limit query int false "Number of orders to return" default(20)
// @Success      200 {object} response.ApiResponse{data=[]mdb.Orders}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/dashboard/recent-orders [get]
func (c *BaseAdminController) RecentOrders(ctx echo.Context) error {
	limit := 20
	if s := ctx.QueryParam("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	rows, err := data.RecentOrders(limit)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, rows)
}

// parseRange maps the ?range=... shorthand to a concrete [start, end].
// Accepted: today / 7d / 30d / custom (with start_at/end_at Unix secs).
// Defaults to today.
func parseRange(ctx echo.Context) (time.Time, time.Time) {
	rng := strings.ToLower(strings.TrimSpace(ctx.QueryParam("range")))
	now := time.Now()
	todayStart := startOfDay(now)
	switch rng {
	case "7d":
		return todayStart.AddDate(0, 0, -6), now
	case "30d":
		return todayStart.AddDate(0, 0, -29), now
	case "custom":
		start := todayStart
		end := now
		if s := ctx.QueryParam("start_at"); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				start = time.Unix(n, 0).In(time.Local)
			}
		}
		if s := ctx.QueryParam("end_at"); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				end = time.Unix(n, 0).In(time.Local)
			}
		}
		return start, end
	default:
		return todayStart, now
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
