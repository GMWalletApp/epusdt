package admin

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/sign"
	"github.com/labstack/echo/v4"
)

// CreateApiKeyRequest is the payload for creating an API key.
// A single key is valid for both gateway flows (epay/gmpay); there is
// no gateway_type. PID is auto-generated (incrementing from 1000);
// no manual override.
type CreateApiKeyRequest struct {
	Name          string `json:"name" validate:"required|maxLen:128" example:"My API Key"`
	IpWhitelist   string `json:"ip_whitelist" example:""`
	NotifyUrl     string `json:"notify_url" example:"https://example.com/notify"`
	GMPaySignMode string `json:"gmpay_sign_mode" enums:"dual,hmac_sha256,md5" example:"hmac_sha256"`
}

// CreateApiKeyResponse is the response for a newly created API key.
type CreateApiKeyResponse struct {
	ID            uint64 `json:"id" example:"1"`
	Name          string `json:"name" example:"My API Key"`
	Pid           string `json:"pid" example:"1003"`
	GMPaySignMode string `json:"gmpay_sign_mode" example:"hmac_sha256"`
	// SecretKey is returned ONCE on creation. After that, fetch via
	// GET /api-keys/:id/secret or rotate to generate a new one.
	SecretKey string `json:"secret_key" example:"secret123abc456"`
}

// UpdateApiKeyRequest is the payload for updating an API key.
type UpdateApiKeyRequest struct {
	Name          *string `json:"name" example:"Updated Key Name"`
	IpWhitelist   *string `json:"ip_whitelist" example:"10.0.0.1,192.168.0.0/24"`
	NotifyUrl     *string `json:"notify_url" example:"https://example.com/notify"`
	GMPaySignMode *string `json:"gmpay_sign_mode" enums:"dual,hmac_sha256,md5" example:"hmac_sha256"`
}

// ChangeApiKeyStatusRequest is the payload for toggling API key status.
type ChangeApiKeyStatusRequest struct {
	// 状态 1=启用 2=禁用
	Status int `json:"status" validate:"required|in:1,2" enums:"1,2" example:"1"`
}

// ListApiKeys returns all api_keys rows. SecretKey is stripped by the
// mdb.ApiKey json tag (`json:"-"`).
// @Summary      List API keys
// @Description  Returns all API keys
// @Tags         Admin API Keys
// @Security     AdminJWT
// @Produce      json
// @Success      200 {object} response.ApiResponse{data=[]mdb.ApiKey}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/api-keys [get]
func (c *BaseAdminController) ListApiKeys(ctx echo.Context) error {
	rows, err := data.ListApiKeys()
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, rows)
}

// CreateApiKey mints a new universal key. PID is auto-incremented from
// the highest existing numeric PID (starting at 1000). Secret is
// randomly generated and returned once.
// @Summary      Create API key
// @Description  Create a new universal API key. Usable for both gateway flows (epay/gmpay). PID auto-incremented; secret returned once.
// @Tags         Admin API Keys
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        request body admin.CreateApiKeyRequest true "API key payload"
// @Success      200 {object} response.ApiResponse{data=admin.CreateApiKeyResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/api-keys [post]
func (c *BaseAdminController) CreateApiKey(ctx echo.Context) error {
	req := new(CreateApiKeyRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	signMode := sign.GMPaySignModeHMACSHA256
	if strings.TrimSpace(req.GMPaySignMode) != "" {
		if !sign.IsExplicitGMPaySignMode(req.GMPaySignMode) {
			return c.FailJson(ctx, constant.ParamsMarshalErr)
		}
		signMode = sign.NormalizeGMPaySignMode(req.GMPaySignMode)
	}

	// Retry on unique-index violation: two concurrent creates could
	// both see the same max PID from NextPid() and race on INSERT.
	// The loser re-reads and retries with the now-higher max.
	const maxAttempts = 5
	secret := randomHex(32)
	var row *mdb.ApiKey
	for attempt := 0; attempt < maxAttempts; attempt++ {
		pid, err := data.NextPid()
		if err != nil {
			return c.FailJson(ctx, err)
		}
		row = &mdb.ApiKey{
			Name:          req.Name,
			Pid:           strconv.Itoa(pid),
			SecretKey:     secret,
			IpWhitelist:   req.IpWhitelist,
			NotifyUrl:     req.NotifyUrl,
			GMPaySignMode: signMode,
			Status:        mdb.ApiKeyStatusEnable,
		}
		err = data.CreateApiKey(row)
		if err == nil {
			break
		}
		if attempt == maxAttempts-1 || !isUniqueViolation(err) {
			return c.FailJson(ctx, err)
		}
	}
	return c.SucJson(ctx, CreateApiKeyResponse{
		ID:            row.ID,
		Name:          row.Name,
		Pid:           row.Pid,
		GMPaySignMode: row.GMPaySignMode,
		SecretKey:     secret,
	})
}

// isUniqueViolation matches the driver-specific strings for a unique-
// index collision across SQLite / MySQL / PostgreSQL. Good enough for
// the PID retry loop; a cleaner approach would be a typed error but
// GORM doesn't surface one portably.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "constraint failed")
}

// UpdateApiKey patches name / ip_whitelist / notify_url. Secret rotation
// and enable/disable have their own endpoints.
// @Summary      Update API key
// @Description  Patch API key name / ip_whitelist / notify_url
// @Tags         Admin API Keys
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        id path int true "API key ID"
// @Param        request body admin.UpdateApiKeyRequest true "Fields to update"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/api-keys/{id} [patch]
func (c *BaseAdminController) UpdateApiKey(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req := new(UpdateApiKeyRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	fields := map[string]interface{}{}
	if req.Name != nil {
		fields["name"] = *req.Name
	}
	if req.IpWhitelist != nil {
		fields["ip_whitelist"] = *req.IpWhitelist
	}
	if req.NotifyUrl != nil {
		fields["notify_url"] = *req.NotifyUrl
	}
	if req.GMPaySignMode != nil {
		if !sign.IsExplicitGMPaySignMode(*req.GMPaySignMode) {
			return c.FailJson(ctx, constant.ParamsMarshalErr)
		}
		fields["gmpay_sign_mode"] = sign.NormalizeGMPaySignMode(*req.GMPaySignMode)
	}
	if err := data.UpdateApiKeyFields(id, fields); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// ChangeApiKeyStatus toggles enable/disable.
// @Summary      Change API key status
// @Description  Toggle enable/disable for an API key (1=enabled, 2=disabled)
// @Tags         Admin API Keys
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        id path int true "API key ID"
// @Param        request body admin.ChangeApiKeyStatusRequest true "Status payload"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/api-keys/{id}/status [post]
func (c *BaseAdminController) ChangeApiKeyStatus(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req := new(ChangeApiKeyStatusRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	if err := data.UpdateApiKeyFields(id, map[string]interface{}{"status": req.Status}); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// RotateApiKeySecret issues a fresh secret_key. Returned only this time.
// @Summary      Rotate API key secret
// @Description  Generate a new secret_key. Returned only once.
// @Tags         Admin API Keys
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "API key ID"
// @Success      200 {object} response.ApiResponse{data=admin.RotateSecretResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/api-keys/{id}/rotate-secret [post]
func (c *BaseAdminController) RotateApiKeySecret(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	secret := randomHex(32)
	if err := data.UpdateApiKeyFields(id, map[string]interface{}{"secret_key": secret}); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, map[string]string{"secret_key": secret})
}

// DeleteApiKey soft-deletes a row. Orders created with this key will
// fail to callback (resolveOrderApiKey returns a clear error) — prefer
// disabling via /status over deletion if there are live orders.
// @Summary      Delete API key
// @Description  Soft-delete an API key
// @Tags         Admin API Keys
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "API key ID"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/api-keys/{id} [delete]
func (c *BaseAdminController) DeleteApiKey(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := data.DeleteApiKeyByID(id); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// GetApiKeyStats returns usage stats for one key. Currently thin — just
// the stored call_count and last_used_at — since we don't persist per-
// call telemetry. Expanded later if/when we add a request log table.
// @Summary      Get API key stats
// @Description  Returns usage stats (call_count, last_used_at) for an API key
// @Tags         Admin API Keys
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "API key ID"
// @Success      200 {object} response.ApiResponse{data=admin.ApiKeyStatsResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/api-keys/{id}/stats [get]
func (c *BaseAdminController) GetApiKeyStats(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	row, err := data.GetApiKeyByID(id)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if row.ID == 0 {
		return c.FailJson(ctx, constant.ApiKeyNotFoundErr)
	}
	return c.SucJson(ctx, map[string]interface{}{
		"call_count":   row.CallCount,
		"last_used_at": row.LastUsedAt,
	})
}

// GetApiKeySecret returns the secret_key for one API key. Requires
// admin JWT authentication.
// @Summary      View API key secret
// @Description  Returns the secret_key for an API key (admin only)
// @Tags         Admin API Keys
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "API key ID"
// @Success      200 {object} response.ApiResponse{data=map[string]string}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/api-keys/{id}/secret [get]
func (c *BaseAdminController) GetApiKeySecret(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	row, err := data.GetApiKeyByID(id)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if row.ID == 0 {
		return c.FailJson(ctx, constant.ApiKeyNotFoundErr)
	}
	return c.SucJson(ctx, map[string]string{"secret_key": row.SecretKey})
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
