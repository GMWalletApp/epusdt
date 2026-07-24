package lang

import (
	"fmt"

	"github.com/GMWalletApp/epusdt/model/data"
)

const (
	SettingKeyLanguage = "lang.language"
	Zh                 = "zh"
	En                 = "en"
)

var zh = map[string]string{
	"select_network":          "请选择要添加的钱包网络",
	"add_wallet":              "请发送 %s 网络收款地址",
	"select_network_first":    "请先选择网络，再发送地址。",
	"wallet_add_fail":         "钱包 [%s] 添加失败：不是合法的 %s 地址",
	"wallet_add_success":      "钱包 [%s] 添加成功（%s）",
	"status_enabled":          "已启用✅",
	"status_disabled":         "已禁用🚫",
	"btn_add_wallet":          "添加钱包地址",
	"read_chains_fail":        "读取支持链失败：",
	"no_available_chains":     "当前没有可用链，请先在后台配置 supported-assets。",
	"btn_refresh":             "刷新列表",
	"select_wallet_prompt":    "请选择钱包继续操作",
	"select_valid_network":    "请选择有效网络",
	"btn_enable":              "启用",
	"btn_disable":             "禁用",
	"btn_delete":              "删除",
	"btn_back":                "返回",
	"wallet_detail":           "网络：%s\n地址：%s",
	"invalid_request":         "请求不合法！",
	"cmd_start_desc":          "开始",
	"notify_template": "🎉 <b>收款成功通知</b>\n\n💰 <b>金额信息</b>\n├ 订单金额：<code>%s %s</code>\n└ 实际到账：<code>%s %s</code>\n\n📋 <b>订单信息</b>\n├ 交易号：<code>%s</code>\n├ 订单号：<code>%s</code>\n├ 网络：<code>%s</code>\n└ 钱包地址：<code>%s</code>\n\n⏰ <b>时间信息</b>\n├ 创建时间：%s\n└ 支付时间：%s",
}

var en = map[string]string{
	"select_network":          "Please select the wallet network to add",
	"add_wallet":              "Please send the %s network receiving address",
	"select_network_first":    "Please select a network first, then send the address.",
	"wallet_add_fail":         "Wallet [%s] add failed: not a valid %s address",
	"wallet_add_success":      "Wallet [%s] added successfully (%s)",
	"status_enabled":          "Enabled✅",
	"status_disabled":         "Disabled🚫",
	"btn_add_wallet":          "Add Wallet",
	"read_chains_fail":        "Failed to read supported chains: ",
	"no_available_chains":     "No available chains. Configure supported-assets in the admin panel first.",
	"btn_refresh":             "Refresh",
	"select_wallet_prompt":    "Select a wallet to continue",
	"select_valid_network":    "Please select a valid network",
	"btn_enable":              "Enable",
	"btn_disable":             "Disable",
	"btn_delete":              "Delete",
	"btn_back":                "Back",
	"wallet_detail":           "Network: %s\nAddress: %s",
	"invalid_request":         "Invalid request!",
	"cmd_start_desc":          "Start",
	"notify_template": "🎉 <b>Payment Received</b>\n\n💰 <b>Amount</b>\n├ Order Amount: <code>%s %s</code>\n└ Actual Received: <code>%s %s</code>\n\n📋 <b>Order</b>\n├ Trade ID: <code>%s</code>\n├ Order ID: <code>%s</code>\n├ Network: <code>%s</code>\n└ Wallet Address: <code>%s</code>\n\n⏰ <b>Time</b>\n├ Created: %s\n└ Paid: %s",
}

var maps = map[string]map[string]string{
	Zh: zh,
	En: en,
}

func current() string {
	return data.GetSettingString(SettingKeyLanguage, Zh)
}

func T(key string) string {
	lang := current()
	m, ok := maps[lang]
	if !ok {
		m = zh
	}
	if v, ok := m[key]; ok {
		return v
	}
	if v, ok := zh[key]; ok {
		return v
	}
	return key
}

func TF(key string, args ...interface{}) string {
	return fmt.Sprintf(T(key), args...)
}
