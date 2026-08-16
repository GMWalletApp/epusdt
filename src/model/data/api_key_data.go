package data

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/util/sign"
	"github.com/dromara/carbon/v2"
	"gorm.io/gorm"
)

// GetEnabledApiKey looks up an enabled merchant credential row by PID.
// A single row is valid for both EPAY and GMPAY flows; the route
// determines the flow. Returns (row, nil) on success; (zero row, nil)
// when no match; error only on true DB failure.
func GetEnabledApiKey(pid string) (*mdb.ApiKey, error) {
	row := new(mdb.ApiKey)
	err := dao.Mdb.Model(row).
		Where("pid = ?", strings.TrimSpace(pid)).
		Where("status = ?", mdb.ApiKeyStatusEnable).
		Limit(1).Find(row).Error
	return row, err
}

// GetApiKeyByID fetches a row by primary key (including disabled).
func GetApiKeyByID(id uint64) (*mdb.ApiKey, error) {
	row := new(mdb.ApiKey)
	err := dao.Mdb.Model(row).Limit(1).Find(row, id).Error
	return row, err
}

// ListApiKeys returns all rows ordered by ID descending.
func ListApiKeys() ([]mdb.ApiKey, error) {
	var rows []mdb.ApiKey
	err := dao.Mdb.Model(&mdb.ApiKey{}).Order("id DESC").Find(&rows).Error
	return rows, err
}

// CreateApiKey persists a new row.
func CreateApiKey(row *mdb.ApiKey) error {
	return dao.Mdb.Create(row).Error
}

// UpdateApiKeyFields updates a whitelist of mutable fields on a row.
func UpdateApiKeyFields(id uint64, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	return dao.Mdb.Model(&mdb.ApiKey{}).Where("id = ?", id).Updates(fields).Error
}

// DeleteApiKeyByID removes (soft-deletes) a row.
func DeleteApiKeyByID(id uint64) error {
	return dao.Mdb.Where("id = ?", id).Delete(&mdb.ApiKey{}).Error
}

// TouchApiKeyUsage increments call_count and stamps last_used_at.
// Called from the signature middleware after a successful verify.
func TouchApiKeyUsage(id uint64) error {
	return dao.Mdb.Model(&mdb.ApiKey{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"call_count":   gorm.Expr("call_count + 1"),
			"last_used_at": carbon.Now().StdTime(),
		}).Error
}

// basePid is the starting PID value for api_keys.
const basePid = 1000

// NextPid returns the next available numeric PID for api_keys. Scans
// ALL rows (including soft-deleted) to avoid reusing a PID whose row
// is still in the table with a non-null deleted_at — the unique index
// on pid doesn't know about soft deletes, so reuse would trigger a
// DB-level unique violation on Create.
func NextPid() (int, error) {
	var rows []mdb.ApiKey
	err := dao.Mdb.Unscoped().Model(&mdb.ApiKey{}).Select("pid").Find(&rows).Error
	if err != nil {
		return basePid, err
	}
	maxPid := basePid - 1
	for _, r := range rows {
		if pid, err := strconv.Atoi(strings.TrimSpace(r.Pid)); err == nil && pid > maxPid {
			maxPid = pid
		}
	}
	return maxPid + 1, nil
}

// SeededApiKey carries the details of a newly created default key so
// the caller can print them to the console (secret is never surfaced
// again via the admin API — it has to be viewed via /api-keys/:id/secret
// after login).
type SeededApiKey struct {
	Pid       string
	Name      string
	SecretKey string
}

// EnsureDefaultApiKey seeds ONE universal API key on a fresh install.
// The seeded key is valid for all three gateway flows — the admin can
// create additional keys later, each also universal. Idempotent: if
// any api_keys row already exists, returns nil without seeding.
func EnsureDefaultApiKey() (*SeededApiKey, error) {
	var count int64
	if err := dao.Mdb.Model(&mdb.ApiKey{}).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, nil
	}
	secret := generateHex(32)
	row := &mdb.ApiKey{
		Name:          "default",
		Pid:           strconv.Itoa(basePid),
		SecretKey:     secret,
		GMPaySignMode: sign.GMPaySignModeHMACSHA256,
		Status:        mdb.ApiKeyStatusEnable,
	}
	if err := dao.Mdb.Create(row).Error; err != nil {
		return nil, err
	}
	return &SeededApiKey{
		Pid:       row.Pid,
		Name:      row.Name,
		SecretKey: secret,
	}, nil
}

func generateHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
