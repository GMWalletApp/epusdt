//go:build !sqlite_cgo

package dao

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func TestPureSQLiteBusyTimeoutAppliesToEveryConnection(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "busy-timeout.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := configureSQLite(db, 2)
	if err != nil {
		t.Fatalf("configure sqlite: %v", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxIdleConns(2)

	ctx := context.Background()
	conn1, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	defer conn1.Close()
	conn2, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	defer conn2.Close()

	for i, conn := range []*sql.Conn{conn1, conn2} {
		var timeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatalf("connection %d busy_timeout: %v", i+1, err)
		}
		if timeout != 5000 {
			t.Fatalf("connection %d busy_timeout = %d, want 5000", i+1, timeout)
		}
	}
}
