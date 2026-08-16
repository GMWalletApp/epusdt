package sign

import (
	"errors"
	"testing"
)

func TestGetHMACSHA256FixedVector(t *testing.T) {
	params := map[string]interface{}{
		"signature": "ignored",
		"pid":       "1000",
		"empty":     "",
		"nil":       nil,
		"name":      "VIP",
		"amount":    100,
	}

	got, err := GetHMACSHA256(params, "test-secret")
	if err != nil {
		t.Fatalf("GetHMACSHA256() error = %v", err)
	}
	const want = "ced9141fab53a83d1178f903e7c22d8a2a7033520f31e319934e92455a008b6c"
	if got != want {
		t.Fatalf("GetHMACSHA256() = %q, want %q", got, want)
	}
}

func TestGetHMACSHA256StructUsesCanonicalParams(t *testing.T) {
	type payload struct {
		PID       string `json:"pid"`
		Name      string `json:"name"`
		Amount    int    `json:"amount"`
		Signature string `json:"signature"`
	}

	got, err := GetHMACSHA256(payload{
		PID:       "1000",
		Name:      "VIP",
		Amount:    100,
		Signature: "ignored",
	}, "test-secret")
	if err != nil {
		t.Fatalf("GetHMACSHA256() error = %v", err)
	}
	const want = "ced9141fab53a83d1178f903e7c22d8a2a7033520f31e319934e92455a008b6c"
	if got != want {
		t.Fatalf("GetHMACSHA256() = %q, want %q", got, want)
	}
}

func TestGetMD5RemainsUnchanged(t *testing.T) {
	params := map[string]interface{}{
		"signature": "ignored",
		"pid":       "1000",
		"name":      "VIP",
		"amount":    100,
	}

	got, err := Get(params, "test-secret")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	const want = "2203e5d2a13264c188f7fac194a632f8"
	if got != want {
		t.Fatalf("Get() = %q, want %q", got, want)
	}
}

func TestVerifyGMPayModes(t *testing.T) {
	params := map[string]interface{}{"pid": "1000", "amount": 100}
	hmacSignature, err := GetHMACSHA256(params, "test-secret")
	if err != nil {
		t.Fatalf("生成 HMAC 签名失败: %v", err)
	}
	md5Signature, err := Get(params, "test-secret")
	if err != nil {
		t.Fatalf("生成 MD5 签名失败: %v", err)
	}

	tests := []struct {
		name      string
		mode      string
		signature string
		want      string
		wantErr   error
	}{
		{name: "双兼容命中 HMAC", mode: GMPaySignModeDual, signature: hmacSignature, want: AlgorithmHMACSHA256},
		{name: "双兼容命中 MD5", mode: GMPaySignModeDual, signature: md5Signature, want: AlgorithmMD5},
		{name: "历史空模式命中 MD5", mode: "", signature: md5Signature, want: AlgorithmMD5},
		{name: "HMAC 模式拒绝 MD5", mode: GMPaySignModeHMACSHA256, signature: md5Signature, wantErr: ErrInvalidSignature},
		{name: "MD5 模式拒绝 HMAC", mode: GMPaySignModeMD5, signature: hmacSignature, wantErr: ErrInvalidSignature},
		{name: "未知模式关闭验签", mode: "unknown", signature: hmacSignature, wantErr: ErrUnsupportedSignMode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := VerifyGMPay(params, "test-secret", tt.signature, tt.mode)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("VerifyGMPay() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("VerifyGMPay() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetByAlgorithmUsesMD5ForHistoricalEmptyValue(t *testing.T) {
	params := map[string]interface{}{"pid": "1000", "amount": 100}
	want, err := Get(params, "test-secret")
	if err != nil {
		t.Fatalf("生成 MD5 签名失败: %v", err)
	}
	got, err := GetByAlgorithm(params, "test-secret", "")
	if err != nil {
		t.Fatalf("GetByAlgorithm() error = %v", err)
	}
	if got != want {
		t.Fatalf("GetByAlgorithm() = %q, want %q", got, want)
	}
}
