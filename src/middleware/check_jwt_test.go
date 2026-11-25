package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GMWalletApp/epusdt/internal/testutil"
	"github.com/GMWalletApp/epusdt/model/dao"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	appjwt "github.com/GMWalletApp/epusdt/util/jwt"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func TestAdminTokenFromAuthorization(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{
			name:   "bearer token",
			header: "Bearer jwt-token",
			want:   "jwt-token",
		},
		{
			name:   "bare token",
			header: "jwt-token",
			want:   "jwt-token",
		},
		{
			name:   "trimmed bare token",
			header: "  jwt-token  ",
			want:   "jwt-token",
		},
		{
			name:    "missing header",
			header:  "",
			wantErr: true,
		},
		{
			name:    "empty bearer token",
			header:  "Bearer ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := adminTokenFromAuthorization(tt.header)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("token = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckAdminJWTAcceptsBearerAndBareTokens(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()

	if err := data.SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyJwtSecret, "test-jwt-secret", mdb.SettingTypeString); err != nil {
		t.Fatalf("seed jwt secret: %v", err)
	}
	user := &mdb.AdminUser{Username: "admin", Status: mdb.AdminUserStatusEnable}
	if err := dao.Mdb.Create(user).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	token, err := appjwt.Sign(user.ID, user.Username, user.SessionVersion)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	tests := []struct {
		name   string
		header string
	}{
		{
			name:   "bearer token",
			header: "Bearer " + token,
		},
		{
			name:   "bare token",
			header: token,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			e.Use(CheckAdminJWT())
			e.GET("/", func(ctx echo.Context) error {
				if got := ctx.Get(AdminUserIDKey); got != user.ID {
					t.Fatalf("%s = %v, want %d", AdminUserIDKey, got, user.ID)
				}
				if got := ctx.Get(AdminUsernameKey); got != "admin" {
					t.Fatalf("%s = %v, want admin", AdminUsernameKey, got)
				}
				return ctx.NoContent(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(echo.HeaderAuthorization, tt.header)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
			}
		})
	}
}

func TestCheckAdminJWTRejectsRevokedAndDisabledUsers(t *testing.T) {
	cleanup := testutil.SetupTestDatabases(t)
	defer cleanup()
	if err := data.SetSetting(mdb.SettingGroupSystem, mdb.SettingKeyJwtSecret, "test-jwt-secret", mdb.SettingTypeString); err != nil {
		t.Fatal(err)
	}
	user := &mdb.AdminUser{Username: "admin", Status: mdb.AdminUserStatusEnable}
	if err := dao.Mdb.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	token, err := appjwt.Sign(user.ID, user.Username, user.SessionVersion)
	if err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.Use(CheckAdminJWT())
	e.GET("/", func(ctx echo.Context) error { return ctx.NoContent(http.StatusNoContent) })
	request := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := request(); got != http.StatusNoContent {
		t.Fatalf("valid token status=%d", got)
	}
	if err := dao.Mdb.Model(user).Update("session_version", gorm.Expr("session_version + 1")).Error; err != nil {
		t.Fatal(err)
	}
	if got := request(); got != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d", got)
	}
	user.SessionVersion++
	token, err = appjwt.Sign(user.ID, user.Username, user.SessionVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := dao.Mdb.Model(user).Update("status", mdb.AdminUserStatusDisable).Error; err != nil {
		t.Fatal(err)
	}
	if got := request(); got != http.StatusUnauthorized {
		t.Fatalf("disabled user's token status=%d", got)
	}
}
