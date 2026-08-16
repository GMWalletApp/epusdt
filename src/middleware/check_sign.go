package middleware

import (
	"bytes"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/util/constant"
	"github.com/GMWalletApp/epusdt/util/json"
	"github.com/GMWalletApp/epusdt/util/sign"
	"github.com/labstack/echo/v4"
)

// 以下上下文键由 CheckApiSign 在 GMPay 验签成功后写入。
// 创建订单处理器使用 API Key 信息和本次实际命中的算法固化订单签名语义。
const (
	ApiKeyIDKey      = "api_key_id"
	ApiKeyRowKey     = "api_key_row"
	SignAlgorithmKey = "gmpay_sign_algorithm"
)

// CheckApiSign 使用请求 pid 对应 API Key 的 secret_key 校验 GMPay 签名。
// 该中间件不处理 EPay；EPay 路由始终执行独立的 MD5 验签。
//
// 流程：
//  1. 读取并回填原始请求体，解析 pid 和 signature。
//  2. 按 pid 查询启用的 API Key，缺失时返回签名错误。
//  3. 按 gmpay_sign_mode 校验 HMAC-SHA256、旧版 MD5 或双兼容模式。
//  4. 校验 IP 白名单，空白名单表示不限制来源。
//  5. 尝试更新调用次数和最后使用时间，不让统计失败中断交易。
//  6. 将 API Key 与实际命中的算法写入上下文，供订单固化后续回调算法。
func CheckApiSign() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			params, err := io.ReadAll(ctx.Request().Body)
			if err != nil {
				return constant.SignatureErr
			}
			// Rewind the body for downstream bindings regardless of outcome.
			ctx.Request().Body = io.NopCloser(bytes.NewBuffer(params))

			m := make(map[string]interface{})
			contentType := ctx.Request().Header.Get("Content-Type")
			if strings.Contains(contentType, "application/x-www-form-urlencoded") {
				values, parseErr := url.ParseQuery(string(params))
				if parseErr != nil {
					return constant.SignatureErr
				}
				for k, vs := range values {
					if len(vs) > 0 {
						m[k] = vs[0]
					}
				}
			} else {
				if err = json.Cjson.Unmarshal(params, &m); err != nil {
					return constant.SignatureErr
				}
			}
			signature, ok := m["signature"]
			if !ok {
				return constant.SignatureErr
			}
			identifier := extractPid(m)
			if identifier == "" {
				return constant.SignatureErr
			}

			row, err := data.GetEnabledApiKey(identifier)
			if err != nil || row.ID == 0 {
				return constant.SignatureErr
			}

			signatureStr, ok := signature.(string)
			if !ok || strings.TrimSpace(signatureStr) == "" {
				return constant.SignatureErr
			}
			algorithm, err := sign.VerifyGMPay(m, row.SecretKey, signatureStr, row.GMPaySignMode)
			if err != nil {
				return constant.SignatureErr
			}

			if !IsIPWhitelisted(row.IpWhitelist, ctx.RealIP()) {
				return constant.SignatureErr
			}

			_ = data.TouchApiKeyUsage(row.ID)

			ctx.Set(ApiKeyIDKey, row.ID)
			ctx.Set(ApiKeyRowKey, row)
			ctx.Set(SignAlgorithmKey, algorithm)
			return next(ctx)
		}
	}
}

// extractPid reads the "pid" field from the request body. JSON numbers
// unmarshal to float64 (EPAY merchants typically send numeric pid); we
// format with -1 precision to drop trailing zeros so "1000" matches
// regardless of whether the client sent a string or a number.
func extractPid(m map[string]interface{}) string {
	v, ok := m["pid"]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// IsIPWhitelisted checks the CSV IP list. Empty list = open.
// Entries may be single IPs or CIDR blocks.
func IsIPWhitelisted(csv, remote string) bool {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return true
	}
	remoteIP := net.ParseIP(remote)
	for _, raw := range strings.Split(csv, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, cidr, err := net.ParseCIDR(entry)
			if err == nil && remoteIP != nil && cidr.Contains(remoteIP) {
				return true
			}
			continue
		}
		if entry == remote {
			return true
		}
	}
	return false
}
