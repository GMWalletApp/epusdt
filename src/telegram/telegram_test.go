package telegram

import (
	"testing"

	"github.com/GMWalletApp/epusdt/config"
	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func withTelegramEnv(token, proxy string, manage int64, fn func()) {
	origToken := config.TgBotToken
	origProxy := config.TgProxy
	origManage := config.TgManage
	defer func() {
		config.TgBotToken = origToken
		config.TgProxy = origProxy
		config.TgManage = origManage
	}()

	config.TgBotToken = token
	config.TgProxy = proxy
	config.TgManage = manage
	fn()
}

func addIndependentPushChannel(t *testing.T) {
	t.Helper()
	if err := dao.Mdb.Create(&mdb.NotificationChannel{
		Name: "Push only", Type: mdb.NotificationTypeTelegram, Enabled: true,
		Config: `{"bot_token":"push-token","chat_id":111}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := data.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCommandBotConfig_IgnoresEnvAndNotificationChannel(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	addIndependentPushChannel(t)

	// Env config is present but settings table is empty —
	// the bot must NOT fall back to config.TgBotToken.
	withTelegramEnv("env-token", "http://127.0.0.1:7890", 123456789, func() {
		cfg, source, err := loadCommandBotConfig()
		if err != nil {
			t.Fatalf("loadCommandBotConfig err: %v", err)
		}
		if cfg != nil {
			t.Fatalf("expected nil config when settings table has no telegram keys, got %+v", cfg)
		}
		if source != "" {
			t.Fatalf("source = %q, want empty", source)
		}
	})
}

func TestLoadCommandBotConfig_UsesSettingsWithIndependentPushChannel(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	addIndependentPushChannel(t)

	// Seed settings table with telegram credentials.
	dao.Mdb.Create(&mdb.Setting{Group: "system", Key: "system.telegram_bot_token", Value: "db-token", Type: "string"})
	dao.Mdb.Create(&mdb.Setting{Group: "system", Key: "system.telegram_chat_id", Value: "987654321", Type: "string"})
	_ = data.ReloadSettings()

	withTelegramEnv("env-token", "", 123456789, func() {
		cfg, source, err := loadCommandBotConfig()
		if err != nil {
			t.Fatalf("loadCommandBotConfig err: %v", err)
		}
		if cfg == nil {
			t.Fatal("expected non-nil config")
		}
		if source != "settings" {
			t.Fatalf("source = %q, want %q", source, "settings")
		}
		if cfg.BotToken != "db-token" {
			t.Fatalf("bot token = %q, want %q", cfg.BotToken, "db-token")
		}
		if cfg.ChatID != 987654321 {
			t.Fatalf("chat id = %d, want %d", cfg.ChatID, int64(987654321))
		}
	})
}

func TestLoadCommandBotConfig_ReturnsNilWithoutCredentials(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	// Fresh DB — ensure the cache doesn't carry over values from a previous test.
	_ = data.ReloadSettings()

	withTelegramEnv("", "", 0, func() {
		cfg, source, err := loadCommandBotConfig()
		if err != nil {
			t.Fatalf("loadCommandBotConfig err: %v", err)
		}
		if cfg != nil {
			t.Fatalf("expected nil config, got %+v", cfg)
		}
		if source != "" {
			t.Fatalf("source = %q, want empty", source)
		}
	})
}

func TestLoadCommandBotConfig_SkipsMissingToken(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	// Only chat_id present, no bot_token → should return nil.
	dao.Mdb.Create(&mdb.Setting{Group: "system", Key: "system.telegram_chat_id", Value: "987654321", Type: "string"})
	_ = data.ReloadSettings()

	cfg, source, err := loadCommandBotConfig()
	if err != nil {
		t.Fatalf("loadCommandBotConfig err: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config when bot_token is absent, got %+v", cfg)
	}
	if source != "" {
		t.Fatalf("source = %q, want empty", source)
	}
}

func TestLoadCommandBotConfig_RejectsInvalidChatID(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	// bot_token present but chat_id is not a valid int → should return error.
	dao.Mdb.Create(&mdb.Setting{Group: "system", Key: "system.telegram_bot_token", Value: "db-token", Type: "string"})
	dao.Mdb.Create(&mdb.Setting{Group: "system", Key: "system.telegram_chat_id", Value: "not-a-number", Type: "string"})
	_ = data.ReloadSettings()

	cfg, _, err := loadCommandBotConfig()
	if err == nil {
		t.Fatal("expected error for invalid chat_id, got nil")
	}
	if cfg != nil {
		t.Fatalf("expected nil config on parse error, got %+v", cfg)
	}
}
