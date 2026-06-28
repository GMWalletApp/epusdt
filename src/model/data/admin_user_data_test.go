package data

import (
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
)

func TestUpsertSettingRowRestoresSoftDeletedSetting(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	row := mdb.Setting{
		Group: mdb.SettingGroupSystem,
		Key:   mdb.SettingKeyInitAdminPasswordChanged,
		Value: "false",
		Type:  mdb.SettingTypeBool,
	}
	if err := upsertSettingRow(dao.Mdb, row); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if err := dao.Mdb.Where("`key` = ?", row.Key).Delete(&mdb.Setting{}).Error; err != nil {
		t.Fatalf("delete setting: %v", err)
	}

	row.Value = "true"
	if err := upsertSettingRow(dao.Mdb, row); err != nil {
		t.Fatalf("restore setting: %v", err)
	}

	var restored mdb.Setting
	if err := dao.Mdb.Where("`key` = ?", row.Key).Take(&restored).Error; err != nil {
		t.Fatalf("load restored setting: %v", err)
	}
	if restored.Value != "true" {
		t.Fatalf("restored value = %q, want true", restored.Value)
	}
	if restored.DeletedAt.Valid {
		t.Fatalf("restored setting still has deleted_at=%v", restored.DeletedAt)
	}
}

func TestGetInitialAdminPasswordKeepsPlaintext(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const password = "init-pass-plain"
	if err := initAdminPasswordState(password); err != nil {
		t.Fatalf("seed initial password state: %v", err)
	}

	got, err := GetInitialAdminPassword()
	if err != nil {
		t.Fatalf("get initial password: %v", err)
	}
	if got != password {
		t.Fatalf("password = %q, want %q", got, password)
	}

	var count int64
	if err := dao.Mdb.Unscoped().
		Model(&mdb.Setting{}).
		Where("`key` = ?", mdb.SettingKeyInitAdminPasswordPlain).
		Count(&count).Error; err != nil {
		t.Fatalf("count plaintext setting: %v", err)
	}
	if count != 1 {
		t.Fatalf("plaintext setting rows after read = %d, want 1", count)
	}
	if GetSettingBool(mdb.SettingKeyInitAdminPasswordFetched, false) {
		t.Fatal("expected fetched flag to stay false before password change")
	}
}

func TestUpdateAdminUserPasswordHardDeletesInitialPasswordPlaintext(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	const (
		oldPassword = "init-pass-plain"
		newPassword = "new-password-123"
	)
	if err := initAdminPasswordState(oldPassword); err != nil {
		t.Fatalf("seed initial password state: %v", err)
	}

	hash, err := HashPassword(oldPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := &mdb.AdminUser{
		Username:     defaultAdminUsername,
		PasswordHash: hash,
		Status:       mdb.AdminUserStatusEnable,
	}
	if err := dao.Mdb.Create(user).Error; err != nil {
		t.Fatalf("seed admin user: %v", err)
	}

	if err := UpdateAdminUserPassword(uint64(user.ID), newPassword); err != nil {
		t.Fatalf("update admin password: %v", err)
	}

	var count int64
	if err := dao.Mdb.Unscoped().
		Model(&mdb.Setting{}).
		Where("`key` = ?", mdb.SettingKeyInitAdminPasswordPlain).
		Count(&count).Error; err != nil {
		t.Fatalf("count plaintext setting: %v", err)
	}
	if count != 0 {
		t.Fatalf("plaintext setting rows after password change = %d, want 0", count)
	}
	if !GetSettingBool(mdb.SettingKeyInitAdminPasswordFetched, false) {
		t.Fatal("expected fetched flag to be true after password change")
	}
}

func TestEnsureDefaultAdminPurgesLegacySoftDeletedPlaintext(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	hash, err := HashPassword("existing-password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.AdminUser{
		Username:     defaultAdminUsername,
		PasswordHash: hash,
		Status:       mdb.AdminUserStatusEnable,
	}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := dao.Mdb.Create(&mdb.Setting{
		Group: mdb.SettingGroupSystem,
		Key:   mdb.SettingKeyInitAdminPasswordPlain,
		Value: "legacy-soft-deleted-plain",
		Type:  mdb.SettingTypeString,
	}).Error; err != nil {
		t.Fatalf("seed plaintext setting: %v", err)
	}
	if err := dao.Mdb.Where("`key` = ?", mdb.SettingKeyInitAdminPasswordPlain).Delete(&mdb.Setting{}).Error; err != nil {
		t.Fatalf("soft delete plaintext setting: %v", err)
	}

	password, created, err := EnsureDefaultAdmin()
	if err != nil {
		t.Fatalf("ensure default admin: %v", err)
	}
	if created || password != "" {
		t.Fatalf("created=%v password=%q, want existing admin unchanged", created, password)
	}

	var count int64
	if err := dao.Mdb.Unscoped().
		Model(&mdb.Setting{}).
		Where("`key` = ?", mdb.SettingKeyInitAdminPasswordPlain).
		Count(&count).Error; err != nil {
		t.Fatalf("count plaintext setting: %v", err)
	}
	if count != 0 {
		t.Fatalf("legacy plaintext rows after ensure = %d, want 0", count)
	}
}
