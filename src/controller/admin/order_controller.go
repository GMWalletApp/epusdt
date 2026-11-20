package admin

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/request"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/log"
	"github.com/labstack/echo/v4"
)

// OrderListResponse wraps the paginated order list.
type OrderListResponse struct {
	List     []mdb.Orders `json:"list"`
	Total    int64        `json:"total" example:"100"`
	Page     int          `json:"page" example:"1"`
	PageSize int          `json:"page_size" example:"20"`
}

// OrderWithSub embeds a parent order together with its child orders.
type OrderWithSub struct {
	mdb.Orders
	SubOrders []mdb.Orders `json:"sub_orders"`
}

// OrderWithSubListResponse wraps the paginated list-with-sub result.
type OrderWithSubListResponse struct {
	List     []OrderWithSub `json:"list"`
	Total    int64          `json:"total" example:"100"`
	Page     int            `json:"page" example:"1"`
	PageSize int            `json:"page_size" example:"20"`
}

// ListOrders handles the admin order list with filters + pagination.
// @Summary      List orders
// @Description  Returns paginated orders with optional filters
// @Tags         Admin Orders
// @Security     AdminJWT
// @Produce      json
// @Param        network query string false "Network filter"
// @Param        token query string false "Token filter"
// @Param        address query string false "Receive address filter"
// @Param        keyword query string false "Keyword search (trade_id or order_id)"
// @Param        status query int false "Order status filter"
// @Param        start_at query int false "Start time (Unix seconds)"
// @Param        end_at query int false "End time (Unix seconds)"
// @Param        page query int false "Page number" default(1)
// @Param        page_size query int false "Page size" default(20)
// @Success      200 {object} response.ApiResponse{data=admin.OrderListResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/orders [get]
func (c *BaseAdminController) ListOrders(ctx echo.Context) error {
	f := parseOrderFilter(ctx)
	rows, total, err := data.ListOrders(f)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, map[string]interface{}{
		"list":      rows,
		"total":     total,
		"page":      f.Page,
		"page_size": f.PageSize,
	})
}

// GetOrder returns full order detail by trade_id.
// @Summary      Get order
// @Description  Returns full order detail by trade_id
// @Tags         Admin Orders
// @Security     AdminJWT
// @Produce      json
// @Param        trade_id path string true "Trade ID"
// @Success      200 {object} response.ApiResponse{data=mdb.Orders}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/orders/{trade_id} [get]
func (c *BaseAdminController) GetOrder(ctx echo.Context) error {
	tradeID := ctx.Param("trade_id")
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if order.ID == 0 {
		return c.FailJson(ctx, constant.OrderNotExists)
	}
	return c.SucJson(ctx, order)
}

// CloseOrder flips a waiting order to expired and releases its lock.
// @Summary      Close order
// @Description  Manually close a waiting order (mark as expired)
// @Tags         Admin Orders
// @Security     AdminJWT
// @Produce      json
// @Param        trade_id path string true "Trade ID"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/orders/{trade_id}/close [post]
func (c *BaseAdminController) CloseOrder(ctx echo.Context) error {
	tradeID := ctx.Param("trade_id")
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if order.ID == 0 {
		return c.FailJson(ctx, constant.OrderNotExists)
	}
	if order.Status != mdb.StatusWaitPay && order.Status != mdb.StatusWaitSelect {
		return c.FailJson(ctx, constant.OrderNotWaitPay)
	}
	ok, err := data.CloseOrderManually(tradeID)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if !ok {
		return c.FailJson(ctx, constant.OrderStatusConflict)
	}
	// Release only concrete on-chain locks. Placeholder orders and provider-only
	// orders do not reserve a local wallet amount.
	if order.Status == mdb.StatusWaitPay &&
		(strings.TrimSpace(order.PayProvider) == "" || order.PayProvider == mdb.PaymentProviderOnChain) &&
		strings.TrimSpace(order.Network) != "" &&
		strings.TrimSpace(order.ReceiveAddress) != "" &&
		strings.TrimSpace(order.Token) != "" &&
		order.ActualAmount > 0 {
		_ = data.UnLockTransaction(order.Network, order.ReceiveAddress, order.Token, order.ActualAmount)
	}
	return c.SucJson(ctx, nil)
}

// MarkOrderPaid manually marks a waiting or expired on-chain order paid
// (operator补单) after verifying the submitted chain transaction.
// @Summary      Mark order paid
// @Description  Manually mark a waiting or expired on-chain order as paid (operator补单)
// @Description  TON accepts canonical ton:<receive_raw>:<lt>:<hash>, lt:hash, or a unique recent hash-only reference for the order receive address.
// @Description  Aptos accepts a transaction hash.
// @Description  Aptos automatic scanning polls fullnode ledger-version transaction ranges with a runtime cursor.
// @Tags         Admin Orders
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        trade_id path string true "Trade ID"
// @Param        request body request.ManualPaymentRequest true "Block transaction ID"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/orders/{trade_id}/mark-paid [post]
func (c *BaseAdminController) MarkOrderPaid(ctx echo.Context) error {
	tradeID := ctx.Param("trade_id")
	adminUserID := currentAdminUserID(ctx)
	req := new(request.ManualPaymentRequest)
	if err := ctx.Bind(req); err != nil {
		log.Sugar.Warnf("[admin-order] mark-paid bind failed admin_user_id=%d trade_id=%s err=%v", adminUserID, tradeID, err)
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req.BlockTransactionId = strings.TrimSpace(req.BlockTransactionId)
	if err := c.ValidateStruct(ctx, req); err != nil {
		log.Sugar.Warnf("[admin-order] mark-paid validation failed admin_user_id=%d trade_id=%s block_transaction_id=%s err=%v", adminUserID, tradeID, req.BlockTransactionId, err)
		return c.FailJson(ctx, err)
	}
	resp, err := service.SubmitManualPayment(tradeID, req.BlockTransactionId)
	if err != nil {
		log.Sugar.Warnf("[admin-order] mark-paid failed admin_user_id=%d trade_id=%s block_transaction_id=%s err=%v", adminUserID, tradeID, req.BlockTransactionId, err)
		return c.FailJson(ctx, err)
	}
	log.Sugar.Infof("[admin-order] mark-paid success admin_user_id=%d trade_id=%s block_transaction_id=%s", adminUserID, tradeID, resp.BlockTransactionId)
	return c.SucJson(ctx, nil)
}

// ResendCallback requeues the order callback by flipping
// callback_confirm back to NO. The mq worker will grab it on the
// next poll tick.
// @Summary      Resend callback
// @Description  Re-queue the order callback notification
// @Tags         Admin Orders
// @Security     AdminJWT
// @Produce      json
// @Param        trade_id path string true "Trade ID"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/orders/{trade_id}/resend-callback [post]
func (c *BaseAdminController) ResendCallback(ctx echo.Context) error {
	tradeID := ctx.Param("trade_id")
	adminUserID := currentAdminUserID(ctx)
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		log.Sugar.Warnf("[admin-order] resend-callback load failed admin_user_id=%d trade_id=%s err=%v", adminUserID, tradeID, err)
		return c.FailJson(ctx, err)
	}
	if order.ID == 0 {
		err = constant.OrderNotExists
		log.Sugar.Warnf("[admin-order] resend-callback rejected admin_user_id=%d trade_id=%s err=%v", adminUserID, tradeID, err)
		return c.FailJson(ctx, err)
	}
	if order.Status != mdb.StatusPaySuccess {
		err = constant.OrderCallbackNotApplicable
		log.Sugar.Warnf("[admin-order] resend-callback rejected admin_user_id=%d trade_id=%s status=%d err=%v", adminUserID, tradeID, order.Status, err)
		return c.FailJson(ctx, err)
	}
	if strings.TrimSpace(order.NotifyUrl) == "" {
		err = constant.OrderNotifyURLEmptyErr
		log.Sugar.Warnf("[admin-order] resend-callback rejected admin_user_id=%d trade_id=%s err=%v", adminUserID, tradeID, err)
		return c.FailJson(ctx, err)
	}
	ok, err := data.ReopenOrderCallback(tradeID)
	if err != nil {
		log.Sugar.Warnf("[admin-order] resend-callback reopen failed admin_user_id=%d trade_id=%s err=%v", adminUserID, tradeID, err)
		return c.FailJson(ctx, err)
	}
	if !ok {
		err = constant.OrderResendCallbackErr
		log.Sugar.Warnf("[admin-order] resend-callback rejected admin_user_id=%d trade_id=%s err=%v", adminUserID, tradeID, err)
		return c.FailJson(ctx, err)
	}
	log.Sugar.Infof("[admin-order] resend-callback queued admin_user_id=%d trade_id=%s", adminUserID, tradeID)
	return c.SucJson(ctx, nil)
}

// ExportOrders streams matching orders as CSV. No pagination — the
// caller drives what rows to include via filter params. Capped at
// 10k rows to keep the response bounded.
// @Summary      Export orders as CSV
// @Description  Stream matching orders as a CSV file (max 10k rows)
// @Tags         Admin Orders
// @Security     AdminJWT
// @Produce      text/csv
// @Param        network query string false "Network filter"
// @Param        token query string false "Token filter"
// @Param        address query string false "Receive address filter"
// @Param        keyword query string false "Keyword search"
// @Param        status query int false "Order status filter"
// @Param        start_at query int false "Start time (Unix seconds)"
// @Param        end_at query int false "End time (Unix seconds)"
// @Success      200 {string} string "CSV file"
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/orders/export [get]
func (c *BaseAdminController) ExportOrders(ctx echo.Context) error {
	f := parseOrderFilter(ctx)
	f.Page = 1
	f.PageSize = 10000
	rows, _, err := data.ListOrders(f)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	resp := ctx.Response()
	resp.Header().Set(echo.HeaderContentType, "text/csv; charset=utf-8")
	resp.Header().Set(echo.HeaderContentDisposition, "attachment; filename=orders.csv")
	resp.WriteHeader(http.StatusOK)

	w := csv.NewWriter(resp.Writer)
	_ = w.Write([]string{"trade_id", "order_id", "amount", "actual_amount", "currency", "token", "network", "receive_address", "status", "block_transaction_id", "created_at"})
	for _, r := range rows {
		_ = w.Write([]string{
			r.TradeId,
			r.OrderId,
			fmt.Sprintf("%.4f", r.Amount),
			fmt.Sprintf("%.4f", r.ActualAmount),
			r.Currency,
			r.Token,
			r.Network,
			r.ReceiveAddress,
			strconv.Itoa(r.Status),
			r.BlockTransactionId,
			r.CreatedAt.ToDateTimeString(),
		})
	}
	w.Flush()
	return nil
}

// parseOrderFilter extracts filter + pagination values from the query
// string. Time params are Unix seconds.
func parseOrderFilter(ctx echo.Context) data.OrderListFilter {
	f := data.OrderListFilter{
		Network: strings.ToLower(strings.TrimSpace(ctx.QueryParam("network"))),
		Token:   strings.ToUpper(strings.TrimSpace(ctx.QueryParam("token"))),
		Address: strings.TrimSpace(ctx.QueryParam("address")),
		Keyword: strings.TrimSpace(ctx.QueryParam("keyword")),
	}
	if s := ctx.QueryParam("status"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			f.Status = n
		}
	}
	if s := ctx.QueryParam("start_at"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			t := time.Unix(n, 0)
			f.StartAt = &t
		}
	}
	if s := ctx.QueryParam("end_at"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			t := time.Unix(n, 0)
			f.EndAt = &t
		}
	}
	if s := ctx.QueryParam("page"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			f.Page = n
		}
	}
	if s := ctx.QueryParam("page_size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			f.PageSize = n
		}
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	return f
}

// ListOrdersWithSub returns the same paginated order list as ListOrders but
// each row has a sub_orders field containing all child orders under it.
// @Summary      List orders with sub-orders
// @Description  Returns paginated orders; each item embeds its child orders in sub_orders
// @Tags         Admin Orders
// @Security     AdminJWT
// @Produce      json
// @Param        network query string false "Network filter"
// @Param        token query string false "Token filter"
// @Param        address query string false "Receive address filter"
// @Param        keyword query string false "Keyword search (trade_id or order_id)"
// @Param        status query int false "Order status filter"
// @Param        start_at query int false "Start time (Unix seconds)"
// @Param        end_at query int false "End time (Unix seconds)"
// @Param        page query int false "Page number" default(1)
// @Param        page_size query int false "Page size" default(20)
// @Success      200 {object} response.ApiResponse{data=admin.OrderWithSubListResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/orders/list-with-sub [get]
func (c *BaseAdminController) ListOrdersWithSub(ctx echo.Context) error {
	f := parseOrderFilter(ctx)
	// Only parent orders at the top level; sub-orders appear nested inside sub_orders.
	f.ParentOnly = true
	rows, total, err := data.ListOrders(f)
	if err != nil {
		return c.FailJson(ctx, err)
	}

	// Batch-fetch all sub-orders for the current page in one query.
	tradeIds := make([]string, 0, len(rows))
	for _, r := range rows {
		tradeIds = append(tradeIds, r.TradeId)
	}
	subs, err := data.GetSubOrdersByParentTradeIds(tradeIds)
	if err != nil {
		return c.FailJson(ctx, err)
	}

	// Group sub-orders by parent_trade_id.
	subMap := make(map[string][]mdb.Orders, len(subs))
	for _, s := range subs {
		subMap[s.ParentTradeId] = append(subMap[s.ParentTradeId], s)
	}

	list := make([]OrderWithSub, 0, len(rows))
	for _, r := range rows {
		children := subMap[r.TradeId]
		if children == nil {
			children = []mdb.Orders{}
		}
		list = append(list, OrderWithSub{Orders: r, SubOrders: children})
	}

	return c.SucJson(ctx, OrderWithSubListResponse{
		List:     list,
		Total:    total,
		Page:     f.Page,
		PageSize: f.PageSize,
	})
}

// GetOrderWithSub returns a single parent order together with all its child
// orders (any status), regardless of whether the caller passes a parent or
// child trade_id — if it is a child, the parent is resolved first.
// @Summary      Get order with sub-orders
// @Description  Returns an order and its child orders embedded in sub_orders
// @Tags         Admin Orders
// @Security     AdminJWT
// @Produce      json
// @Param        trade_id path string true "Trade ID"
// @Success      200 {object} response.ApiResponse{data=admin.OrderWithSub}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/orders/{trade_id}/with-sub [get]
func (c *BaseAdminController) GetOrderWithSub(ctx echo.Context) error {
	tradeID := ctx.Param("trade_id")
	order, err := data.GetOrderInfoByTradeId(tradeID)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if order.ID == 0 {
		return c.FailJson(ctx, constant.OrderNotExists)
	}
	subOrders, err := data.GetAllSubOrders(tradeID)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if subOrders == nil {
		subOrders = []mdb.Orders{}
	}
	return c.SucJson(ctx, OrderWithSub{Orders: *order, SubOrders: subOrders})
}
