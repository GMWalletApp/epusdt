package middleware

import (
	"bytes"
	"crypto/subtle"
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

// Context keys populated by CheckApiSign after successful verification.
// Handlers (pay/order creation) pull ApiKeyIDKey to stamp order.api_key_id.
const (
	ApiKeyIDKey  = "api_key_id"
	ApiKeyRowKey = "api_key_row"
)

// CheckApiSign validates the body signature against the secret_key of
// the api_keys row matching the submitted "pid" field. A single row is
// valid for all gateway flows — identification is always by pid.
//
// Flow:
//  1. Extract the pid from the request body.
//  2. Look up the enabled row by pid; if missing, return signature error.
//  3. Verify signature == lowercase hex HMAC-SHA256(sorted_params, secret_key).
//  4. Enforce IP whitelist (empty = allow any).
//  5. Bump call_count / last_used_at (best-effort).
//  6. Stash api_key_id + row in context and rewind the body.
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

			checkSignature, err := sign.GetHMACSHA256(m, row.SecretKey)
			if err != nil {
				return constant.SignatureErr
			}
			signatureStr, _ := signature.(string)
			if subtle.ConstantTimeCompare([]byte(checkSignature), []byte(signatureStr)) != 1 {
				return constant.SignatureErr
			}

			if !IsIPWhitelisted(row.IpWhitelist, ctx.RealIP()) {
				return constant.SignatureErr
			}

			_ = data.TouchApiKeyUsage(row.ID)

			ctx.Set(ApiKeyIDKey, row.ID)
			ctx.Set(ApiKeyRowKey, row)
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
