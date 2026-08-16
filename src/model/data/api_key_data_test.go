package data

import (
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/sign"
)

func TestEnsureDefaultApiKeyUsesHMACSHA256(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := dao.Mdb.Unscoped().Where("1 = 1").Delete(&mdb.ApiKey{}).Error; err != nil {
		t.Fatalf("清理测试 API Key 失败: %v", err)
	}
	seeded, err := EnsureDefaultApiKey()
	if err != nil {
		t.Fatalf("创建默认 API Key 失败: %v", err)
	}
	if seeded == nil {
		t.Fatal("未返回新建的默认 API Key")
	}
	row, err := GetEnabledApiKey(seeded.Pid)
	if err != nil {
		t.Fatalf("读取默认 API Key 失败: %v", err)
	}
	if row.GMPaySignMode != sign.GMPaySignModeHMACSHA256 {
		t.Fatalf("默认 API Key 模式 = %q, want %q", row.GMPaySignMode, sign.GMPaySignModeHMACSHA256)
	}
}
