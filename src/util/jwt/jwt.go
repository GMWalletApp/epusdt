package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/golang-jwt/jwt/v4"
)

// DefaultExpiration is the token lifetime for admin sessions.
const DefaultExpiration = 24 * time.Hour

// AdminClaims is the JWT payload for admin sessions.
type AdminClaims struct {
	AdminUserID    uint64 `json:"uid"`
	Username       string `json:"usr"`
	SessionVersion uint64 `json:"ver"`
	jwt.RegisteredClaims
}

// EnsureSecret reads system.jwt_secret from settings; if absent,
// generates a new 32-byte random hex string and persists it. Called once
// at startup so subsequent sign/verify can assume the secret exists.
func EnsureSecret() (string, error) {
	secret := data.GetSettingString(mdb.SettingKeyJwtSecret, "")
	if secret != "" {
		return secret, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret = hex.EncodeToString(buf)
	if err := data.SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyJwtSecret, secret, mdb.SettingTypeString); err != nil {
		return "", err
	}
	return secret, nil
}

// Sign returns a signed JWT for the given admin user.
func Sign(userID uint64, username string, sessionVersion uint64) (string, error) {
	secret, err := EnsureSecret()
	if err != nil {
		return "", err
	}
	claims := AdminClaims{
		AdminUserID:    userID,
		Username:       username,
		SessionVersion: sessionVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(DefaultExpiration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Parse validates a token string and returns its claims.
func Parse(tokenStr string) (*AdminClaims, error) {
	secret, err := EnsureSecret()
	if err != nil {
		return nil, err
	}
	claims := &AdminClaims{}
	_, err = jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}
