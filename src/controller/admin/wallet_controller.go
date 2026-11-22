package admin

import (
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/labstack/echo/v4"
)

// AdminAddWalletRequest is the payload for adding a wallet via admin.
type AdminAddWalletRequest struct {
	Network string `json:"network" validate:"required" example:"tron"`
	Address string `json:"address" validate:"required" example:"TA4Wt1DUCqz6YegbnsmqsWC5uUfbdBqPxm"`
	Remark  string `json:"remark" example:"主钱包"`
}

// AdminUpdateWalletRequest is the payload for updating a wallet remark.
type AdminUpdateWalletRequest struct {
	Remark *string `json:"remark" example:"已更新的备注"`
}

// AdminChangeWalletStatusRequest is the payload for toggling wallet status.
type AdminChangeWalletStatusRequest struct {
	// 状态 1=启用 2=禁用
	Status int `json:"status" validate:"required|in:1,2" enums:"1,2" example:"1"`
}

// AdminBatchImportRequest is the payload for batch importing wallets.
type AdminBatchImportRequest struct {
	Network string `json:"network" validate:"required" example:"tron"`
	// Addresses listed as observation-only wallets. Private-key import
	// and API-managed modes are out of scope for v1.
	Addresses []string `json:"addresses" validate:"required" example:"TA4Wt1DUCqz6YegbnsmqsWC5uUfbdBqPxm,TA9pkx4DFxrEw8JZzUtyDrh2uAat1LDuJL"`
}

// WalletListItem extends WalletAddress with order count.
type WalletListItem struct {
	mdb.WalletAddress
	OrderCount int64 `json:"order_count" example:"5"`
}

// AdminListWallets extends the read model with per-wallet order counts
// so the UI can render the "订单数" column without an N+1 follow-up.
// @Summary      List wallets (admin)
// @Description  Returns wallets with per-wallet order counts
// @Tags         Admin Wallets
// @Security     AdminJWT
// @Produce      json
// @Param        network query string false "Network filter"
// @Param        status query int false "Status filter (1=enabled, 2=disabled)"
// @Param        keyword query string false "Search by address or remark"
// @Success      200 {object} response.ApiResponse{data=[]admin.WalletListItem}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/wallets [get]
func (c *BaseAdminController) AdminListWallets(ctx echo.Context) error {
	network := strings.ToLower(strings.TrimSpace(ctx.QueryParam("network")))
	status := strings.TrimSpace(ctx.QueryParam("status"))
	keyword := strings.TrimSpace(ctx.QueryParam("keyword"))

	tx := dao.Mdb.Model(&mdb.WalletAddress{})
	if network != "" {
		tx = tx.Where("network = ?", network)
	}
	if status != "" {
		if s, err := strconv.Atoi(status); err == nil {
			tx = tx.Where("status = ?", s)
		}
	}
	if keyword != "" {
		kw := "%" + keyword + "%"
		tx = tx.Where("address LIKE ? OR remark LIKE ?", kw, kw)
	}
	var rows []mdb.WalletAddress
	if err := tx.Order("id DESC").Find(&rows).Error; err != nil {
		return c.FailJson(ctx, err)
	}

	counts, err := data.CountOrdersByAddress()
	if err != nil {
		return c.FailJson(ctx, err)
	}
	out := make([]WalletListItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, WalletListItem{WalletAddress: r, OrderCount: counts[r.Address]})
	}
	return c.SucJson(ctx, out)
}

// AdminAddWallet wraps the existing create with admin-exposed Remark +
// Source=manual so audit fields are populated.
// @Summary      Add wallet (admin)
// @Description  Create a wallet with admin-specific fields (remark, source=manual)
// @Tags         Admin Wallets
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        request body admin.AdminAddWalletRequest true "Wallet payload"
// @Success      200 {object} response.ApiResponse{data=mdb.WalletAddress}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/wallets [post]
func (c *BaseAdminController) AdminAddWallet(ctx echo.Context) error {
	req := new(AdminAddWalletRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	row, err := data.AddWalletAddressWithNetwork(req.Network, req.Address)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	// Patch remark/source separately to avoid changing the existing
	// package-level AddWalletAddressWithNetwork signature (used by
	// Telegram bot + legacy endpoint).
	fields := map[string]interface{}{"source": mdb.WalletSourceManual}
	if req.Remark != "" {
		fields["remark"] = req.Remark
	}
	_ = dao.Mdb.Model(&mdb.WalletAddress{}).Where("id = ?", row.ID).Updates(fields).Error
	row.Remark = req.Remark
	row.Source = mdb.WalletSourceManual
	return c.SucJson(ctx, row)
}

// AdminGetWallet returns detail including order count for one wallet.
// @Summary      Get wallet (admin)
// @Description  Returns wallet detail with order count
// @Tags         Admin Wallets
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "Wallet ID"
// @Success      200 {object} response.ApiResponse{data=admin.WalletListItem}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/wallets/{id} [get]
func (c *BaseAdminController) AdminGetWallet(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	wallet, err := data.GetWalletAddressById(id)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if wallet.ID == 0 {
		return c.FailJson(ctx, constant.WalletNotFoundErr)
	}
	counts, _ := data.CountOrdersByAddress()
	return c.SucJson(ctx, WalletListItem{WalletAddress: *wallet, OrderCount: counts[wallet.Address]})
}

// AdminUpdateWallet only patches remark (status has its own endpoint).
// @Summary      Update wallet (admin)
// @Description  Patch wallet remark
// @Tags         Admin Wallets
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        id path int true "Wallet ID"
// @Param        request body admin.AdminUpdateWalletRequest true "Fields to update"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/wallets/{id} [patch]
func (c *BaseAdminController) AdminUpdateWallet(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req := new(AdminUpdateWalletRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	fields := map[string]interface{}{}
	if req.Remark != nil {
		fields["remark"] = *req.Remark
	}
	if len(fields) == 0 {
		return c.SucJson(ctx, nil)
	}
	if err := dao.Mdb.Model(&mdb.WalletAddress{}).Where("id = ?", id).Updates(fields).Error; err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// AdminChangeWalletStatus reuses existing helper.
// @Summary      Change wallet status (admin)
// @Description  Toggle enable/disable for a wallet (1=enabled, 2=disabled)
// @Tags         Admin Wallets
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        id path int true "Wallet ID"
// @Param        request body admin.AdminChangeWalletStatusRequest true "Status payload"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/wallets/{id}/status [post]
func (c *BaseAdminController) AdminChangeWalletStatus(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req := new(AdminChangeWalletStatusRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	if err := data.ChangeWalletAddressStatus(id, req.Status); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// AdminDeleteWallet soft-deletes the wallet.
// @Summary      Delete wallet (admin)
// @Description  Soft-delete a wallet
// @Tags         Admin Wallets
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "Wallet ID"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/wallets/{id} [delete]
func (c *BaseAdminController) AdminDeleteWallet(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := data.DeleteWalletAddressById(id); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// AdminBatchImportWallets accepts a list of addresses and creates
// them all as observation wallets. Per-row failures are collected and
// returned so the UI can surface which addresses already existed.
// @Summary      Batch import wallets
// @Description  Import multiple wallet addresses at once. Per-row status is returned; failed rows include error_code for frontend i18n.
// @Tags         Admin Wallets
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        request body admin.AdminBatchImportRequest true "Batch import payload"
// @Success      200 {object} response.ApiResponse{data=[]admin.BatchImportResult}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/wallets/batch-import [post]
func (c *BaseAdminController) AdminBatchImportWallets(ctx echo.Context) error {
	req := new(AdminBatchImportRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	type result struct {
		Address   string `json:"address"`
		OK        bool   `json:"ok"`
		ErrorCode int    `json:"error_code,omitempty"`
		Error     string `json:"error,omitempty"`
	}
	out := make([]result, 0, len(req.Addresses))
	for _, addr := range req.Addresses {
		row, err := data.AddWalletAddressWithNetwork(req.Network, addr)
		if err != nil {
			code, msg := constant.ResolveErrno(err)
			out = append(out, result{Address: addr, OK: false, ErrorCode: code, Error: msg})
			continue
		}
		_ = dao.Mdb.Model(&mdb.WalletAddress{}).Where("id = ?", row.ID).
			Update("source", mdb.WalletSourceImport).Error
		out = append(out, result{Address: addr, OK: true})
	}
	return c.SucJson(ctx, out)
}
