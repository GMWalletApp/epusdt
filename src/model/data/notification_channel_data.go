package data

import (
	"encoding/json"
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

// ListNotificationChannels returns all rows, optionally filtered by type.
func ListNotificationChannels(channelType string) ([]mdb.NotificationChannel, error) {
	var rows []mdb.NotificationChannel
	tx := dao.Mdb.Model(&mdb.NotificationChannel{})
	if channelType != "" {
		tx = tx.Where("type = ?", strings.ToLower(channelType))
	}
	err := tx.Order("id DESC").Find(&rows).Error
	return rows, err
}

// ListEnabledChannelsByEvent returns enabled rows whose Events JSON has
// the named event key set to true.
func ListEnabledChannelsByEvent(event string) ([]mdb.NotificationChannel, error) {
	var rows []mdb.NotificationChannel
	if err := dao.Mdb.Model(&mdb.NotificationChannel{}).
		Where("enabled = ?", true).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		events := map[string]bool{}
		if r.Events != "" {
			_ = json.Unmarshal([]byte(r.Events), &events)
		}
		if events[event] {
			out = append(out, r)
		}
	}
	return out, nil
}

// GetFirstEnabledTelegramChannel returns the oldest enabled telegram
// row, or nil ID if none. Telegram bot startup uses this as the primary
// instance backing /start and command handlers.
func GetFirstEnabledTelegramChannel() (*mdb.NotificationChannel, error) {
	row := new(mdb.NotificationChannel)
	err := dao.Mdb.Model(row).
		Where("type = ?", mdb.NotificationTypeTelegram).
		Where("enabled = ?", true).
		Order("id ASC").
		Limit(1).Find(row).Error
	return row, err
}

// GetNotificationChannelByID fetches by PK.
func GetNotificationChannelByID(id uint64) (*mdb.NotificationChannel, error) {
	row := new(mdb.NotificationChannel)
	err := dao.Mdb.Model(row).Limit(1).Find(row, id).Error
	return row, err
}

// CreateNotificationChannel inserts a new row.
func CreateNotificationChannel(row *mdb.NotificationChannel) error {
	return dao.Mdb.Create(row).Error
}

// UpdateNotificationChannelFields patches mutable columns.
func UpdateNotificationChannelFields(id uint64, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return dao.Mdb.Model(&mdb.NotificationChannel{}).Where("id = ?", id).Updates(fields).Error
}

// DeleteNotificationChannelByID soft-deletes a row.
func DeleteNotificationChannelByID(id uint64) error {
	return dao.Mdb.Where("id = ?", id).Delete(&mdb.NotificationChannel{}).Error
}
