package data

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/dromara/carbon/v2"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultAdminUsername = "admin"
const InitialAdminPasswordHashAlgorithm = "sha256"

var (
	ErrInitAdminPasswordUnavailable    = errors.New("initial password unavailable")
	ErrInitAdminPasswordAlreadyFetched = errors.New("initial password already fetched")
)

// InitialAdminPasswordHashInfo is returned by the hash query endpoint
// so the frontend can detect whether the operator is still using the
// initial password.
type InitialAdminPasswordHashInfo struct {
	Algorithm       string `json:"algorithm" example:"sha256"`
	PasswordHash    string `json:"password_hash" example:"3f79bb7b435b05321651daefd374cdc9f5f72c467ea3f9f3c5f6e6d7e8f9a0b1"`
	PasswordChanged bool   `json:"password_changed" example:"false"`
	Available       bool   `json:"available" example:"true"`
}

// EnsureDefaultAdmin seeds an initial admin account when no admin user
// exists. The password is randomly generated and returned so the caller
// can print it to the console. Idempotent — subsequent calls return
// ("", false, nil).
func EnsureDefaultAdmin() (password string, created bool, err error) {
	if err := purgeDeletedInitialAdminPasswordPlain(); err != nil {
		return "", false, err
	}
	var count int64
	if err := dao.Mdb.Model(&mdb.AdminUser{}).Count(&count).Error; err != nil {
		return "", false, err
	}
	if count > 0 {
		return "", false, nil
	}
	password = randomAdminPassword()
	hash, err := HashPassword(password)
	if err != nil {
		return "", false, err
	}
	user := &mdb.AdminUser{
		Username:     defaultAdminUsername,
		PasswordHash: hash,
		Status:       mdb.AdminUserStatusEnable,
	}
	if err := dao.Mdb.Create(user).Error; err != nil {
		return "", false, err
	}
	if err := initAdminPasswordState(password); err != nil {
		// Account was already created; surface both for visibility while
		// preserving created=true and password for emergency fallback.
		return password, true, err
	}
	return password, true, nil
}

func purgeDeletedInitialAdminPasswordPlain() error {
	return dao.Mdb.Unscoped().
		Where("`key` = ? AND deleted_at IS NOT NULL", mdb.SettingKeyInitAdminPasswordPlain).
		Delete(&mdb.Setting{}).Error
}

func randomAdminPassword() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// HashInitialAdminPassword fingerprints the initial plaintext password so
// the frontend can compare user input locally without exposing plaintext.
func HashInitialAdminPassword(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return fmt.Sprintf("%x", sum[:])
}

func initAdminPasswordState(plain string) error {
	hash := HashInitialAdminPassword(plain)
	settings := []mdb.Setting{
		{
			Group: mdb.SettingGroupSystem, Key: mdb.SettingKeyInitAdminPasswordPlain,
			Value: plain, Type: mdb.SettingTypeString,
			Description: "One-time readable initial admin password",
		},
		{
			Group: mdb.SettingGroupSystem, Key: mdb.SettingKeyInitAdminPasswordHash,
			Value: hash, Type: mdb.SettingTypeString,
			Description: "SHA-256 fingerprint for initial admin password",
		},
		{
			Group: mdb.SettingGroupSystem, Key: mdb.SettingKeyInitAdminPasswordFetched,
			Value: "false", Type: mdb.SettingTypeBool,
			Description: "Whether initial admin password plaintext has been cleared",
		},
		{
			Group: mdb.SettingGroupSystem, Key: mdb.SettingKeyInitAdminPasswordChanged,
			Value: "false", Type: mdb.SettingTypeBool,
			Description: "Whether initial admin password has been changed",
		},
	}
	for _, row := range settings {
		if err := upsertSettingRow(dao.Mdb, row); err != nil {
			return err
		}
	}
	// Keep in-process cache coherent for the current process.
	settingsCacheMu.Lock()
	settingsCache[mdb.SettingKeyInitAdminPasswordPlain] = plain
	settingsCache[mdb.SettingKeyInitAdminPasswordHash] = hash
	settingsCache[mdb.SettingKeyInitAdminPasswordFetched] = "false"
	settingsCache[mdb.SettingKeyInitAdminPasswordChanged] = "false"
	settingsCacheMu.Unlock()
	return nil
}

func upsertSettingRow(tx *gorm.DB, row mdb.Setting) error {
	updates := clause.AssignmentColumns([]string{"group", "value", "type", "description", "updated_at"})
	updates = append(updates, clause.Assignment{Column: clause.Column{Name: "deleted_at"}, Value: nil})
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: updates,
	}).Create(&row).Error
}

// GetInitialAdminPassword returns the initial admin password plaintext while
// it is still available. The plaintext remains readable until the admin
// password is changed, after which the stored plaintext is deleted.
func GetInitialAdminPassword() (string, error) {
	row := new(mdb.Setting)
	if err := dao.Mdb.Model(&mdb.Setting{}).
		Where("`key` = ?", mdb.SettingKeyInitAdminPasswordPlain).
		Limit(1).
		Find(row).Error; err != nil {
		return "", err
	}
	if row.ID != 0 && row.Value != "" {
		return row.Value, nil
	}

	var fetched mdb.Setting
	if err := dao.Mdb.Model(&mdb.Setting{}).
		Where("`key` = ?", mdb.SettingKeyInitAdminPasswordFetched).
		Limit(1).
		Find(&fetched).Error; err != nil {
		return "", err
	}
	if strings.EqualFold(strings.TrimSpace(fetched.Value), "true") {
		return "", ErrInitAdminPasswordAlreadyFetched
	}
	return "", ErrInitAdminPasswordUnavailable
}

func clearInitialAdminPasswordPlain(tx *gorm.DB) error {
	res := tx.Unscoped().
		Where("`key` = ?", mdb.SettingKeyInitAdminPasswordPlain).
		Delete(&mdb.Setting{})
	if res.Error != nil {
		return res.Error
	}
	if err := upsertSettingRow(tx, mdb.Setting{
		Group:       mdb.SettingGroupSystem,
		Key:         mdb.SettingKeyInitAdminPasswordFetched,
		Value:       "true",
		Type:        mdb.SettingTypeBool,
		Description: "Whether initial admin password plaintext has been cleared",
	}); err != nil {
		return err
	}
	return nil
}

// GetInitialAdminPasswordHashInfo returns the initial-password fingerprint
// and the changed-state flag used by the admin frontend.
func GetInitialAdminPasswordHashInfo() (*InitialAdminPasswordHashInfo, error) {
	hash := strings.TrimSpace(GetSettingString(mdb.SettingKeyInitAdminPasswordHash, ""))
	if hash == "" {
		return &InitialAdminPasswordHashInfo{
			Algorithm:       InitialAdminPasswordHashAlgorithm,
			PasswordHash:    "",
			PasswordChanged: true,
			Available:       false,
		}, nil
	}
	return &InitialAdminPasswordHashInfo{
		Algorithm:       InitialAdminPasswordHashAlgorithm,
		PasswordHash:    hash,
		PasswordChanged: GetSettingBool(mdb.SettingKeyInitAdminPasswordChanged, false),
		Available:       true,
	}, nil
}

// IsUsingInitialAdminPassword reports whether the current admin password is
// still considered the seeded initial password.
func IsUsingInitialAdminPassword() bool {
	hash := strings.TrimSpace(GetSettingString(mdb.SettingKeyInitAdminPasswordHash, ""))
	if hash == "" {
		return false
	}
	return !GetSettingBool(mdb.SettingKeyInitAdminPasswordChanged, false)
}

// HashPassword bcrypts a plaintext password.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword compares a plaintext password against a bcrypt hash.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// GetAdminUserByUsername returns the row for a username (case-insensitive).
func GetAdminUserByUsername(username string) (*mdb.AdminUser, error) {
	u := new(mdb.AdminUser)
	err := dao.Mdb.Model(u).
		Where("username = ?", strings.ToLower(strings.TrimSpace(username))).
		Limit(1).Find(u).Error
	return u, err
}

// GetAdminUserByID returns the row for an ID.
func GetAdminUserByID(id uint64) (*mdb.AdminUser, error) {
	u := new(mdb.AdminUser)
	err := dao.Mdb.Model(u).Limit(1).Find(u, id).Error
	return u, err
}

// UpdateAdminUserPassword rehashes and persists a new password. When the
// change succeeds, the stored initial-password plaintext is deleted so it can
// no longer be fetched from the public bootstrap route.
func UpdateAdminUserPassword(id uint64, newPlain string) error {
	hash, err := HashPassword(newPlain)
	if err != nil {
		return err
	}
	changedCacheValue := ""
	err = dao.Mdb.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&mdb.AdminUser{}).
			Where("id = ?", id).
			Update("password_hash", hash).Error; err != nil {
			return err
		}

		// Old installs may not have initial-password metadata; skip in
		// that case to keep password updates backward compatible.
		var initHashRow mdb.Setting
		if err := tx.Model(&mdb.Setting{}).
			Where("`key` = ?", mdb.SettingKeyInitAdminPasswordHash).
			Limit(1).
			Find(&initHashRow).Error; err != nil {
			return err
		}
		initHash := strings.TrimSpace(initHashRow.Value)
		if initHash == "" {
			return nil
		}

		newHash := HashInitialAdminPassword(newPlain)
		changed := subtle.ConstantTimeCompare([]byte(initHash), []byte(newHash)) != 1
		changedValue := "true"
		if !changed {
			changedValue = "false"
		}
		if err := upsertSettingRow(tx, mdb.Setting{
			Group:       mdb.SettingGroupSystem,
			Key:         mdb.SettingKeyInitAdminPasswordChanged,
			Value:       changedValue,
			Type:        mdb.SettingTypeBool,
			Description: "Whether initial admin password has been changed",
		}); err != nil {
			return err
		}
		if err := clearInitialAdminPasswordPlain(tx); err != nil {
			return err
		}
		changedCacheValue = changedValue
		return nil
	})
	if err != nil {
		return err
	}
	if changedCacheValue != "" {
		settingsCacheMu.Lock()
		delete(settingsCache, mdb.SettingKeyInitAdminPasswordPlain)
		settingsCache[mdb.SettingKeyInitAdminPasswordFetched] = "true"
		settingsCache[mdb.SettingKeyInitAdminPasswordChanged] = changedCacheValue
		settingsCacheMu.Unlock()
	}
	return nil
}

// TouchAdminUserLastLogin stamps last_login_at to now.
func TouchAdminUserLastLogin(id uint64) error {
	return dao.Mdb.Model(&mdb.AdminUser{}).
		Where("id = ?", id).
		Update("last_login_at", carbon.Now().StdTime()).Error
}
