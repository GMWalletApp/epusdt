package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/util/log"
	tb "gopkg.in/telebot.v3"
	"gopkg.in/telebot.v3/middleware"
)

// bots is the single bot instance backing slash-command handlers
// (/start, WalletList, add-wallet dialog). Config is read from the
// settings table (system.telegram_bot_token + system.telegram_chat_id).
// Decoupled from notification_channels — that table drives push
// notifications only (via notify/dispatcher.go).
var (
	bots        *tb.Bot
	adminChatID int64
	reloadMu    sync.Mutex
	reloadWG    sync.WaitGroup
)

// BotStart connects the command bot. If no telegram channel is
// configured yet it logs and returns; the service otherwise runs fine
// and notifications still fan out as soon as a channel is added (those
// use their own per-request bot; see notify/telegram_sender.go).
func BotStart() {
	if err := reloadBot("bootstrap"); err != nil {
		log.Sugar.Errorf("[telegram] bot start failed: %v", err)
	}
}

// ReloadBotAsync refreshes the command bot from notification_channels.
// It is used by admin API handlers after telegram channel create/update/
// status/delete so operators don't need to restart the service.
func ReloadBotAsync(reason string) {
	reloadWG.Add(1)
	go func() {
		defer reloadWG.Done()
		if err := reloadBot(reason); err != nil {
			log.Sugar.Errorf("[telegram] reload failed, reason=%s err=%v", reason, err)
		}
	}()
}

// WaitForReloadsForTest 等待已经提交的异步重载完成，供测试隔离全局状态时使用。
func WaitForReloadsForTest() {
	reloadWG.Wait()
}

// loadCommandBotConfig reads the command-bot config from the settings
// table (system.telegram_bot_token + system.telegram_chat_id).
// Returns (nil, "", nil) when the keys are absent so the caller can
// log and return gracefully — bot stays disabled until settings are set.
func loadCommandBotConfig() (*botConfig, string, error) {
	botToken := strings.TrimSpace(data.GetSettingString("system.telegram_bot_token", ""))
	chatIDStr := strings.TrimSpace(data.GetSettingString("system.telegram_chat_id", ""))
	if botToken == "" || chatIDStr == "" {
		return nil, "", nil
	}
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		return nil, "", fmt.Errorf("invalid telegram chat_id in settings: %w", err)
	}
	return &botConfig{BotToken: botToken, ChatID: chatID}, "settings", nil
}

// botConfig holds the minimal fields needed to start the command bot.
type botConfig struct {
	BotToken string
	ChatID   int64
}

func reloadBot(reason string) error {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	if bots != nil {
		bots.Stop()
		bots = nil
	}

	cfg, source, err := loadCommandBotConfig()
	if err != nil {
		return err
	}
	if cfg == nil {
		log.Sugar.Infof("[telegram] no enabled telegram channel configured; command bot disabled (reason=%s)", reason)
		return nil
	}
	adminChatID = cfg.ChatID

	botSetting := tb.Settings{
		Token:       cfg.BotToken,
		Poller:      &tb.LongPoller{Timeout: 50 * time.Second},
		Synchronous: true,
	}
	newBot, err := tb.NewBot(botSetting)
	if err != nil {
		return fmt.Errorf("new bot: %w", err)
	}
	if err = newBot.SetCommands(Cmds); err != nil {
		return fmt.Errorf("set commands: %w", err)
	}
	bots = newBot
	RegisterHandle()
	go bots.Start()

	log.Sugar.Infof("[telegram] command bot started, reason=%s, source=%s", reason, source)
	return nil
}

// RegisterHandle wires slash-command and text-message routes. The
// admin whitelist uses the chat_id from the same channel row.
func RegisterHandle() {
	if bots == nil {
		return
	}
	adminOnly := bots.Group()
	adminOnly.Use(middleware.Whitelist(adminChatID))
	adminOnly.Handle(START_CMD, WalletList)
	adminOnly.Handle(tb.OnText, OnTextMessageHandle)
}
