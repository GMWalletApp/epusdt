package sign

import "testing"

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
