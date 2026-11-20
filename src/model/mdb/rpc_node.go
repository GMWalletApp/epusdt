package mdb

import "github.com/dromara/carbon/v2"

const (
	RpcNodeTypeHttp = "http"
	RpcNodeTypeWs   = "ws"
	RpcNodeTypeLite = "lite"
)

const (
	RpcNodeStatusUnknown = "unknown"
	RpcNodeStatusOk      = "ok"
	RpcNodeStatusDown    = "down"
)

const (
	RpcNodePurposeGeneral      = "general"
	RpcNodePurposeManualVerify = "manual_verify"
	RpcNodePurposeBoth         = "both"
)

// RpcNode records one RPC endpoint for a network. Runtime selection filters by
// Type, Status and Purpose so paid manual-verification nodes stay isolated.
type RpcNode struct {
	Network string `gorm:"column:network;size:32;index:rpc_nodes_network_status_index,priority:1" json:"network" example:"tron"`
	Url     string `gorm:"column:url;size:512" json:"url" example:"https://api.trongrid.io"`
	// 连接类型 http=HTTP请求 ws=WebSocket长连接 lite=TON liteserver配置
	Type    string `gorm:"column:type;size:16" json:"type" enums:"http,ws,lite" example:"http"`
	Weight  int    `gorm:"column:weight;default:1" json:"weight" example:"1"`
	ApiKey  string `gorm:"column:api_key;size:255" json:"api_key" example:"your-api-key"`
	Enabled bool   `gorm:"column:enabled;default:true" json:"enabled" example:"true"`
	// 用途 general=通用 manual_verify=补单专用 both=通用+补单
	Purpose string `gorm:"column:purpose;size:32;default:general;index:rpc_nodes_purpose_index" json:"purpose" enums:"general,manual_verify,both" example:"general"`
	// 健康状态 unknown=未知 ok=正常 down=异常
	Status        string      `gorm:"column:status;size:16;default:unknown;index:rpc_nodes_network_status_index,priority:2" json:"status" enums:"unknown,ok,down" example:"ok"`
	LastLatencyMs int         `gorm:"column:last_latency_ms;default:-1" json:"last_latency_ms" example:"120"`
	LastCheckedAt carbon.Time `gorm:"column:last_checked_at" json:"last_checked_at" example:"2026-04-16 12:00:00"`
	BaseModel
}

func (r *RpcNode) TableName() string {
	return "rpc_nodes"
}
