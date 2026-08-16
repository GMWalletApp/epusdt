package mdb

import "github.com/dromara/carbon/v2"

const (
	ApiKeyStatusEnable  = 1
	ApiKeyStatusDisable = 2
)

// ApiKey stores a universal merchant credential (PID + secret) that is
// valid for both EPAY and GMPAY payment flows. Identification at
// request time is always by PID; the gateway flow is chosen by the
// route (not by any property of the row).
type ApiKey struct {
	Name        string `gorm:"column:name;size:128" json:"name" example:"My API Key"`
	Pid         string `gorm:"column:pid;size:128;uniqueIndex:api_keys_pid_uindex" json:"pid" example:"1000"`
	SecretKey   string `gorm:"column:secret_key;size:255" json:"-"`
	IpWhitelist string `gorm:"column:ip_whitelist;type:text" json:"ip_whitelist" example:"192.168.1.0/24,10.0.0.1"`
	NotifyUrl   string `gorm:"column:notify_url;size:512" json:"notify_url" example:"https://example.com/notify"`
	// GMPaySignMode 控制 GMPay 接口允许的签名算法；EPay 始终使用 MD5。
	GMPaySignMode string `gorm:"column:gmpay_sign_mode;size:32;not null;default:dual" json:"gmpay_sign_mode" enums:"dual,hmac_sha256,md5" example:"hmac_sha256"`
	// 状态 1=启用 2=禁用
	Status     int         `gorm:"column:status;default:1" json:"status" enums:"1,2" example:"1"`
	CallCount  int64       `gorm:"column:call_count;default:0" json:"call_count" example:"342"`
	LastUsedAt carbon.Time `gorm:"column:last_used_at" json:"last_used_at" example:"2026-04-16 12:00:00"`
	BaseModel
}

func (a *ApiKey) TableName() string {
	return "api_keys"
}
