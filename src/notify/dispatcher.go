// Package notify fans out domain events (order paid, order expired,
// etc.) to every NotificationChannel subscribed to the event. Senders
// are type-keyed (telegram/webhook/email/...) so adding a new push
// method means registering one function; no callers change.
package notify

import (
	"encoding/json"
	"sync"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/log"
)

// Sender delivers an already-rendered text message via a channel row.
// Config is the channel's Config JSON string (type-specific shape).
type Sender func(config, text string) error

var (
	sendersMu  sync.RWMutex
	senders    = map[string]Sender{}
	dispatchWG sync.WaitGroup
)

// RegisterSender wires a Sender for a channel type. Called once at
// package init of each sender file (e.g. telegram_sender.go).
func RegisterSender(channelType string, s Sender) {
	sendersMu.Lock()
	senders[channelType] = s
	sendersMu.Unlock()
}

func getSender(channelType string) (Sender, bool) {
	sendersMu.RLock()
	defer sendersMu.RUnlock()
	s, ok := senders[channelType]
	return s, ok
}

// Dispatch delivers text to every enabled channel subscribed to event.
// Per-channel failures are logged but don't abort the fan-out — one
// broken TG token shouldn't prevent webhook delivery.
func Dispatch(event, text string) {
	channels, err := data.ListEnabledChannelsByEvent(event)
	if err != nil {
		log.Sugar.Errorf("[notify] list channels failed: %v", err)
		return
	}
	for _, ch := range channels {
		sender, ok := getSender(ch.Type)
		if !ok {
			log.Sugar.Warnf("[notify] no sender registered for type=%s (channel_id=%d)", ch.Type, ch.ID)
			continue
		}
		dispatchWG.Add(1)
		go func(c mdb.NotificationChannel) {
			defer dispatchWG.Done()
			if err := sender(c.Config, text); err != nil {
				log.Sugar.Errorf("[notify] send failed type=%s channel_id=%d: %v", c.Type, c.ID, err)
			}
		}(ch)
	}
}

// WaitForDispatchesForTest 等待已经提交的异步通知发送完成，供测试隔离全局状态时使用。
func WaitForDispatchesForTest() {
	dispatchWG.Wait()
}

// ParseConfig helper for senders: unmarshal channel Config JSON into
// an arbitrary struct, returning a typed error on invalid JSON.
func ParseConfig(raw string, out interface{}) error {
	if raw == "" {
		return ErrEmptyConfig
	}
	return json.Unmarshal([]byte(raw), out)
}
