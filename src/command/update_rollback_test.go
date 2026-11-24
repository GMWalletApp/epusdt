package command

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GMWalletApp/epusdt/internal/selfupdate"
)

func TestStartupDatabaseFailureRestoresPendingUpdate(t *testing.T) {
	if os.Getenv("EPUSDT_TEST_ROLLBACK_CHILD") == "1" {
		os.Args = []string{"epusdt", "http", "start"}
		if err := Execute(http.Dir(".")); err != nil {
			t.Fatal(err)
		}
		return
	}
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ field, stage string }{
		{"sqlite_database_filename", "primary database"},
		{"runtime_sqlite_filename", "runtime database"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "epusdt")
			// The child must see its own isolated executable path during rollback.
			if err := os.Link(image, path); err != nil {
				t.Fatal(err)
			}
			config := "install=false\ndb_type=sqlite\n" + tc.field + "=" + dir + "\n"
			for name, content := range map[string]string{".env": config, "epusdt.update-backup": "previous image", "epusdt.update-pending": "v2.1.4"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(path, "-test.run=^TestStartupDatabaseFailureRestoresPendingUpdate$")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "EPUSDT_TEST_ROLLBACK_CHILD=1")
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != selfupdate.RestartExitCode() || !strings.Contains(string(out), tc.stage) {
				t.Fatalf("exit %v, output %s", err, out)
			}
			b, err := os.ReadFile(path)
			if err != nil || string(b) != "previous image" {
				t.Fatal("previous executable was not restored")
			}
			for _, suffix := range []string{".update-pending", ".update-backup"} {
				if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s still present: %v", suffix, err)
				}
			}
			var outcome selfupdate.State
			b, err = os.ReadFile(path + ".update-result")
			if err != nil || json.Unmarshal(b, &outcome) != nil || outcome.Status != "failed" || outcome.Stage != "startup" || outcome.TargetVersion != "v2.1.4" || !strings.Contains(outcome.Error, tc.stage) {
				t.Fatalf("missing startup diagnostic: %s, %v", b, err)
			}
		})
	}
}
