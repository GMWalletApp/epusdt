package admin

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/task"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/labstack/echo/v4"
)

// CreateRpcNodeRequest is the payload for creating an RPC node.
type CreateRpcNodeRequest struct {
	Network string `json:"network" validate:"required" example:"tron"`
	Url     string `json:"url" validate:"required" example:"https://api.trongrid.io"`
	// 连接类型 http=HTTP请求 ws=WebSocket长连接 lite=TON liteserver配置
	Type    string `json:"type" validate:"required|in:http,ws,lite" enums:"http,ws,lite" example:"http"`
	Weight  int    `json:"weight" example:"1"`
	ApiKey  string `json:"api_key" example:""`
	Enabled *bool  `json:"enabled" example:"true"`
	// 用途 general=通用 manual_verify=补单专用 both=通用+补单
	Purpose string `json:"purpose" enums:"general,manual_verify,both" example:"general"`
}

// UpdateRpcNodeRequest is the payload for updating an RPC node.
type UpdateRpcNodeRequest struct {
	Url     *string `json:"url" example:"https://api.trongrid.io"`
	Weight  *int    `json:"weight" example:"1"`
	ApiKey  *string `json:"api_key" example:"your-api-key"`
	Enabled *bool   `json:"enabled" example:"true"`
	Purpose *string `json:"purpose" enums:"general,manual_verify,both" example:"manual_verify"`
}

// ListRpcNodes returns rows optionally filtered by network.
// @Summary      List RPC nodes
// @Description  Returns RPC nodes, optionally filtered by network
// @Tags         Admin RPC Nodes
// @Security     AdminJWT
// @Produce      json
// @Param        network query string false "Network filter"
// @Success      200 {object} response.ApiResponse{data=[]mdb.RpcNode}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/rpc-nodes [get]
func (c *BaseAdminController) ListRpcNodes(ctx echo.Context) error {
	network := strings.ToLower(strings.TrimSpace(ctx.QueryParam("network")))
	rows, err := data.ListRpcNodes(network)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, rows)
}

// CreateRpcNode inserts a row. Status starts as "unknown" until the
// health-check task runs.
// @Summary      Create RPC node
// @Description  Create a new RPC node
// @Tags         Admin RPC Nodes
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        request body admin.CreateRpcNodeRequest true "RPC node payload"
// @Success      200 {object} response.ApiResponse{data=mdb.RpcNode}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/rpc-nodes [post]
func (c *BaseAdminController) CreateRpcNode(ctx echo.Context) error {
	req := new(CreateRpcNodeRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := c.ValidateStruct(ctx, req); err != nil {
		return c.FailJson(ctx, err)
	}
	weight := req.Weight
	if weight < 1 {
		weight = 1
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	purpose, err := rpcNodePurposeFromRequest(req.Purpose)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	nodeURL := strings.TrimSpace(req.Url)
	nodeType := strings.ToLower(strings.TrimSpace(req.Type))
	if err := validateRpcNodeURLForType(nodeURL, nodeType); err != nil {
		return c.FailJson(ctx, err)
	}
	row := &mdb.RpcNode{
		Network:       strings.ToLower(strings.TrimSpace(req.Network)),
		Url:           nodeURL,
		Type:          nodeType,
		Weight:        weight,
		ApiKey:        req.ApiKey,
		Enabled:       enabled,
		Purpose:       purpose,
		Status:        mdb.RpcNodeStatusUnknown,
		LastLatencyMs: -1,
	}
	if err := data.CreateRpcNode(row); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, row)
}

// UpdateRpcNode patches url/weight/api_key/enabled/purpose.
// @Summary      Update RPC node
// @Description  Patch RPC node fields
// @Tags         Admin RPC Nodes
// @Security     AdminJWT
// @Accept       json
// @Produce      json
// @Param        id path int true "RPC node ID"
// @Param        request body admin.UpdateRpcNodeRequest true "Fields to update"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/rpc-nodes/{id} [patch]
func (c *BaseAdminController) UpdateRpcNode(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	req := new(UpdateRpcNodeRequest)
	if err := ctx.Bind(req); err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	fields := map[string]interface{}{}
	if req.Url != nil {
		row, err := data.GetRpcNodeByID(id)
		if err != nil {
			return c.FailJson(ctx, err)
		}
		if row.ID == 0 {
			return c.FailJson(ctx, constant.RpcNodeNotFoundErr)
		}
		nodeURL := strings.TrimSpace(*req.Url)
		if err := validateRpcNodeURLForType(nodeURL, row.Type); err != nil {
			return c.FailJson(ctx, err)
		}
		fields["url"] = nodeURL
	}
	if req.Weight != nil {
		fields["weight"] = *req.Weight
	}
	if req.ApiKey != nil {
		fields["api_key"] = *req.ApiKey
	}
	if req.Enabled != nil {
		fields["enabled"] = *req.Enabled
	}
	if req.Purpose != nil {
		purpose, err := rpcNodePurposeFromRequest(*req.Purpose)
		if err != nil {
			return c.FailJson(ctx, err)
		}
		fields["purpose"] = purpose
	}
	if err := data.UpdateRpcNodeFields(id, fields); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// DeleteRpcNode soft-deletes the row.
// @Summary      Delete RPC node
// @Description  Soft-delete an RPC node
// @Tags         Admin RPC Nodes
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "RPC node ID"
// @Success      200 {object} response.ApiResponse
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/rpc-nodes/{id} [delete]
func (c *BaseAdminController) DeleteRpcNode(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	if err := data.DeleteRpcNodeByID(id); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, nil)
}

// HealthCheckRpcNode performs an on-demand probe and writes the result.
// For HTTP/WS/lite endpoints this is a TCP-level check against the configured
// URL host. TON lite rows usually point to a global.config.json HTTPS URL.
// @Summary      Health check RPC node
// @Description  Perform an on-demand health probe on an RPC node
// @Tags         Admin RPC Nodes
// @Security     AdminJWT
// @Produce      json
// @Param        id path int true "RPC node ID"
// @Success      200 {object} response.ApiResponse{data=admin.RpcHealthCheckResponse}
// @Failure      400 {object} response.ApiResponse
// @Router       /admin/api/v1/rpc-nodes/{id}/health-check [post]
func (c *BaseAdminController) HealthCheckRpcNode(ctx echo.Context) error {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil {
		return c.FailJson(ctx, constant.ParamsMarshalErr)
	}
	row, err := data.GetRpcNodeByID(id)
	if err != nil {
		return c.FailJson(ctx, err)
	}
	if row.ID == 0 {
		return c.FailJson(ctx, constant.RpcNodeNotFoundErr)
	}
	status, latency := task.ProbeNode(row.Url)
	if err := data.UpdateRpcNodeHealth(id, status, latency); err != nil {
		return c.FailJson(ctx, err)
	}
	return c.SucJson(ctx, map[string]interface{}{
		"status":          status,
		"last_latency_ms": latency,
	})
}

func rpcNodePurposeFromRequest(raw string) (string, error) {
	purpose := strings.ToLower(strings.TrimSpace(raw))
	if purpose == "" {
		return mdb.RpcNodePurposeGeneral, nil
	}
	switch purpose {
	case mdb.RpcNodePurposeGeneral, mdb.RpcNodePurposeManualVerify, mdb.RpcNodePurposeBoth:
		return purpose, nil
	default:
		return "", constant.RpcNodePurposeErr
	}
}

func validateRpcNodeURLForType(rawURL string, nodeType string) error {
	rawURL = strings.TrimSpace(rawURL)
	nodeType = strings.ToLower(strings.TrimSpace(nodeType))
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return constant.RpcNodeURLErr
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch nodeType {
	case mdb.RpcNodeTypeHttp:
		if scheme == "http" || scheme == "https" {
			return nil
		}
		return constant.RpcNodeHTTPURLErr
	case mdb.RpcNodeTypeWs:
		if scheme == "ws" || scheme == "wss" {
			return nil
		}
		return constant.RpcNodeWebSocketURLErr
	case mdb.RpcNodeTypeLite:
		if scheme == "http" || scheme == "https" {
			return nil
		}
		return constant.RpcNodeHTTPURLErr
	default:
		return constant.RpcNodeTypeErr
	}
}
