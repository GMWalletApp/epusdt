package admin

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/notify"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/labstack/echo/v4"
)

// CreateNotificationChannelRequest is the payload for creating a notification channel.
type CreateNotificationChannelRequest struct {
	Type    string                 `json:"type" validate:"required|in:telegram,webhook,email" enums:"telegram,webhook,email" example:"telegram"`
	Name    string                 `json:"name" validate:"required|maxLen:128" example:"主通知群"`
	Config  map[string]interface{} `json:"config" validate:"required"`
	Events  interface{}            `json:"events"`
	Enabled *bool                  `json:"enabled" example:"true"`
}

// UpdateNotificationChannelRequest is the payload for updating a notification channel.
type UpdateNotificationChannelRequest struct {
	Name    *string                `json:"name" example:"更新后的名称"`
	Config  map[string]interface{} `json:"config"`
	Events  interface{}            `json:"events"`
	Enabled *bool                  `json:"enabled" example:"true"`
}

// ChangeChannelStatusRequest is the payload for toggling notification channel status.
type ChangeChannelStatusRequest struct {
	Enabled bool `json:"enabled" example:"true"`
}

// ListNotificationChannels lists rows, optionally filtered by type.
// @Summary      List notification channels
// @Description  Returns notification channels, optionally filtered by type
// @Tags         Admin Notifications
// @Security     AdminJWT
// @Produce      json
// @Param        type query string false "Channel type: telegram, webhook, email"
// @Success      200 {object} response.ApiResponse{data=[]mdb.NotificationChannel}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/notification-channels [get]
func (c *BaseAdminController) ListNotificationChannels(ctx echo.Context) error {
	channelType := strings.ToLower(strings.TrimSpace(ctx.QueryParam("type")))
	rows, err := data.ListNotificationChannels(channelType)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, rows)
}

// CreateNotificationChannel validates Config shape per type (only
// telegram is strongly validated today) and inserts a row.
// @Summary      Create notification channel
// @Description  Create a new notification channel
// @Tags         Admin Notifications
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        request body admin.CreateNotificationChannelRequest true "Channel payload"
// @Success      200 {object} response.ApiResponse{data=mdb.NotificationChannel}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/notification-channels [post]
func (c *BaseAdminController) CreateNotificationChannel(ctx echo.Context) error {
	req := new(CreateNotificationChannelRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	if err := validateChannelConfig(req.Type, req.Config); err != nil {
		return c.FailJson(ctx, err)
	}
	eventsMap, err := normalizeChannelEvents(req.Events)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	configJSON, _ := json.Marshal(req.Config)
	eventsJSON, _ := json.Marshal(eventsMap)

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row := &mdb.NotificationChannel{
		Type:    req.Type,
		Name:    req.Name,
		Config:  string(configJSON),
		Events:  string(eventsJSON),
		Enabled: enabled,
	}
	if err := data.CreateNotificationChannel(row); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, row)
}

// UpdateNotificationChannel patches name/config/events/enabled.
// @Summary      Update notification channel
// @Description  Patch notification channel fields
// @Tags         Admin Notifications
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        id path int true "Channel ID"
// @Param        request body admin.UpdateNotificationChannelRequest true "Fields to update"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/notification-channels/{id} [patch]
func (c *BaseAdminController) UpdateNotificationChannel(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req := new(UpdateNotificationChannelRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	fields := map[string]interface{}{}
	if req.Name != nil {
		fields["name"] = *req.Name
	}
	if req.Config != nil {
		configJSON, _ := json.Marshal(req.Config)
		fields["config"] = string(configJSON)
	}
	if req.Events != nil {
		eventsMap, err := normalizeChannelEvents(req.Events)
		if err != nil {
			return c.FailJson(ctx, err)
		}
		eventsJSON, _ := json.Marshal(eventsMap)
		fields["events"] = string(eventsJSON)
	}
	if req.Enabled != nil {
		fields["enabled"] = *req.Enabled
	}
	if err := data.UpdateNotificationChannelFields(id, fields); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// ChangeNotificationChannelStatus toggles enabled.
// @Summary      Change notification channel status
// @Description  Toggle enable/disable for a notification channel
// @Tags         Admin Notifications
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        id path int true "Channel ID"
// @Param        request body admin.ChangeChannelStatusRequest true "Status payload"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/notification-channels/{id}/status [post]
func (c *BaseAdminController) ChangeNotificationChannelStatus(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req := new(ChangeChannelStatusRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := data.UpdateNotificationChannelFields(id, map[string]interface{}{"enabled": req.Enabled}); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// DeleteNotificationChannel soft-deletes the row.
// @Summary      Delete notification channel
// @Description  Soft-delete a notification channel
// @Tags         Admin Notifications
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "Channel ID"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/notification-channels/{id} [delete]
func (c *BaseAdminController) DeleteNotificationChannel(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := data.DeleteNotificationChannelByID(id); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// validateChannelConfig enforces minimum shape. Telegram needs bot_token
// + chat_id; webhook/email kept loose until those senders exist.
func validateChannelConfig(channelType string, cfg map[string]interface{}) error {
	if cfg == nil {
		return constant.NotificationConfigErr
	}
	if channelType == mdb.NotificationTypeTelegram {
		configJSON, _ := json.Marshal(cfg)
		if _, err := notify.ParseTelegramConfig(string(configJSON)); err != nil {
			return constant.NotificationConfigErr
		}
	}
	return nil
}

func normalizeChannelEvents(raw interface{}) (map[string]bool, error) {
	if raw == nil {
		return map[string]bool{}, nil
	}

	toFlag := func(v interface{}) (bool, error) {
		switch t := v.(type) {
		case bool:
			return t, nil
		case float64:
			return t != 0, nil
		case string:
			s := strings.ToLower(strings.TrimSpace(t))
			switch s {
			case "", "0", "false", "off", "no":
				return false, nil
			case "1", "true", "on", "yes":
				return true, nil
			default:
				return false, constant.NotificationEventsErr
			}
		default:
			return false, constant.NotificationEventsErr
		}
	}

	trimKey := func(s string) string {
		return strings.TrimSpace(s)
	}

	out := make(map[string]bool)
	switch t := raw.(type) {
	case map[string]bool:
		for k, v := range t {
			k = trimKey(k)
			if k == "" {
				continue
			}
			out[k] = v
		}
		return out, nil
	case map[string]interface{}:
		for k, v := range t {
			k = trimKey(k)
			if k == "" {
				continue
			}
			flag, err := toFlag(v)
			if err != nil {
				return nil, err
			}
			out[k] = flag
		}
		return out, nil
	case []interface{}:
		for _, v := range t {
			s, ok := v.(string)
			if !ok {
				return nil, constant.NotificationEventsErr
			}
			s = trimKey(s)
			if s == "" {
				continue
			}
			out[s] = true
		}
		return out, nil
	default:
		return nil, constant.NotificationEventsErr
	}
}
