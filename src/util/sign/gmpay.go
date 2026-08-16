package sign

import (
	"crypto/subtle"
	"errors"
	"strings"
)

const (
	AlgorithmMD5        = "md5"
	AlgorithmHMACSHA256 = "hmac_sha256"

	GMPaySignModeDual       = "dual"
	GMPaySignModeMD5        = AlgorithmMD5
	GMPaySignModeHMACSHA256 = AlgorithmHMACSHA256
)

var (
	ErrInvalidSignature     = errors.New("签名校验失败")
	ErrUnsupportedSignMode  = errors.New("不支持的签名模式")
	ErrUnsupportedAlgorithm = errors.New("不支持的签名算法")
)

// NormalizeGMPaySignMode 统一 GMPay 签名模式；历史空值按双兼容处理。
func NormalizeGMPaySignMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return GMPaySignModeDual
	case GMPaySignModeDual:
		return GMPaySignModeDual
	case GMPaySignModeMD5:
		return GMPaySignModeMD5
	case GMPaySignModeHMACSHA256:
		return GMPaySignModeHMACSHA256
	default:
		return ""
	}
}

// IsExplicitGMPaySignMode 判断管理接口传入的非空模式是否有效。
func IsExplicitGMPaySignMode(mode string) bool {
	return strings.TrimSpace(mode) != "" && NormalizeGMPaySignMode(mode) != ""
}

// NormalizeAlgorithm 统一订单签名算法；升级前订单和历史空值按 MD5 处理。
func NormalizeAlgorithm(algorithm string) string {
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case "", AlgorithmMD5:
		return AlgorithmMD5
	case AlgorithmHMACSHA256:
		return AlgorithmHMACSHA256
	default:
		return ""
	}
}

// GetByAlgorithm 按订单保存的算法生成签名，未知算法直接失败。
func GetByAlgorithm(data interface{}, bizKey, algorithm string) (string, error) {
	switch NormalizeAlgorithm(algorithm) {
	case AlgorithmMD5:
		return Get(data, bizKey)
	case AlgorithmHMACSHA256:
		return GetHMACSHA256(data, bizKey)
	default:
		return "", ErrUnsupportedAlgorithm
	}
}

// VerifyGMPay 按商户配置验签，并返回本次实际命中的算法。
func VerifyGMPay(data interface{}, bizKey, providedSignature, mode string) (string, error) {
	mode = NormalizeGMPaySignMode(mode)
	if mode == "" {
		return "", ErrUnsupportedSignMode
	}

	verify := func(algorithm string) (bool, error) {
		expected, err := GetByAlgorithm(data, bizKey, algorithm)
		if err != nil {
			return false, err
		}
		return subtle.ConstantTimeCompare([]byte(expected), []byte(providedSignature)) == 1, nil
	}

	if mode == GMPaySignModeDual || mode == GMPaySignModeHMACSHA256 {
		ok, err := verify(AlgorithmHMACSHA256)
		if err != nil {
			return "", err
		}
		if ok {
			return AlgorithmHMACSHA256, nil
		}
	}
	if mode == GMPaySignModeDual || mode == GMPaySignModeMD5 {
		ok, err := verify(AlgorithmMD5)
		if err != nil {
			return "", err
		}
		if ok {
			return AlgorithmMD5, nil
		}
	}

	return "", ErrInvalidSignature
}
