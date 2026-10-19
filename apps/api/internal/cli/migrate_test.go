package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// writeBackup creates a pg_dump custom-format backup of size bytes (at least the signature, unless
// size is 0) last modified at mtime.
func writeBackup(t *testing.T, size int, mtime time.Time) string {
	t.Helper()
	data := make([]byte, size)
	copy(data, pgDumpMagic)
	return writeFile(t, data, mtime)
}

// writeFile creates a file holding data last modified at mtime.
func writeFile(t *testing.T, data []byte, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tally.dump")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckBackup(t *testing.T) {
	at := time.Now()
	fresh := writeBackup(t, 10, at.Add(-10*time.Minute))
	stale := writeBackup(t, 10, at.Add(-2*time.Hour))
	empty := writeBackup(t, 0, at)
	signatureOnly := writeBackup(t, len(pgDumpMagic), at)
	plainSQL := writeFile(t, []byte("-- PostgreSQL database dump\n"), at)
	truncated := writeFile(t, []byte("PGD"), at)
	cases := []struct {
		name    string
		file    string
		wantErr string
	}{
		{"without backup", "", "without --backup-file"},
		{"missing file", filepath.Join(t.TempDir(), "nope.dump"), "no such file"},
		{"directory", t.TempDir(), "not a non-empty regular file"},
		{"empty file", empty, "not a non-empty regular file"},
		{"plain SQL dump", plainSQL, "not a pg_dump custom-format archive"},
		{"shorter than the signature", truncated, "not a pg_dump custom-format archive"},
		{"stale backup", stale, "take a fresh backup"},
		{"fresh backup", fresh, ""},
		{"signature only", signatureOnly, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkBackup(tc.file, at)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestBackupRequired(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"unset APP_ENV is guarded", map[string]string{}, true},
		{"blank APP_ENV is guarded", map[string]string{"APP_ENV": " "}, true},
		{"production is guarded", map[string]string{"APP_ENV": "production"}, true},
		{"explicit development is waived", map[string]string{"APP_ENV": "development"}, false},
		{"explicit test is waived", map[string]string{"APP_ENV": "test"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := mapLookup(tc.env)
			base, err := config.LoadBase(lookup)
			if err != nil {
				t.Fatal(err)
			}
			if got := backupRequired(lookup, base); got != tc.want {
				t.Errorf("backupRequired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMigrateProductionGuardRunsBeforeConnecting(t *testing.T) {
	for _, env := range []map[string]string{{"APP_ENV": "production"}, {}} {
		for _, args := range [][]string{{"migrate", "up"}, {"migrate", "down"}, {"migrate", "down", "--all"}, {"migrate", "force", "3"}} {
			_, err := run(t, env, args...)
			if err == nil || !strings.Contains(err.Error(), "without --backup-file") {
				t.Errorf("%v with env %v = %v, want backup guard error", args, env, err)
			}
		}
	}
	env := map[string]string{"APP_ENV": "production"}
	// version only reads; it is not guarded and fails later on the missing connection string.
	if _, err := run(t, env, "migrate", "version"); err == nil || !strings.Contains(err.Error(), "MIGRATE_DATABASE_URL") {
		t.Errorf("version in production = %v, want MIGRATE_DATABASE_URL error", err)
	}
	// A fresh backup passes the guard; the connection string is still required.
	backup := writeBackup(t, 10, time.Now())
	if _, err := run(t, env, "migrate", "up", "--backup-file", backup); err == nil || !strings.Contains(err.Error(), "MIGRATE_DATABASE_URL") {
		t.Errorf("up with backup = %v, want MIGRATE_DATABASE_URL error", err)
	}
	// Without migrations the command stops after the guard and before opening any connection.
	setMigrations(t, nil)
	env["MIGRATE_DATABASE_URL"] = "postgres://owner@localhost/tally"
	if _, err := run(t, env, "migrate", "up", "--backup-file", backup); !errors.Is(err, db.ErrNoMigrations) {
		t.Errorf("up with backup and URL = %v, want ErrNoMigrations", err)
	}
}

func TestMigrateRejectsInvalidConfiguration(t *testing.T) {
	if _, err := run(t, map[string]string{"APP_ENV": "staging"}, "migrate", "up"); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Errorf("invalid APP_ENV = %v", err)
	}
	if _, err := run(t, map[string]string{"APP_ENV": "development"}, "migrate", "up"); err == nil || !strings.Contains(err.Error(), "MIGRATE_DATABASE_URL") {
		t.Errorf("missing URL = %v", err)
	}
	setMigrations(t, fstest.MapFS{"000001_x.up.sql": {Data: []byte("SELECT 1")}})
	_, err := run(t, map[string]string{"MIGRATE_DATABASE_URL": "not a url"}, "migrate", "version")
	if err == nil || !strings.Contains(err.Error(), "not a valid") || strings.Contains(err.Error(), "not a url") {
		t.Errorf("invalid URL = %v, want a message that does not echo the URL", err)
	}
}

func TestMigrateArguments(t *testing.T) {
	env := map[string]string{"MIGRATE_DATABASE_URL": "postgres://owner@localhost/tally"}
	cases := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"migrate", "down", "0"}, "positive integer"},
		{[]string{"migrate", "down", "x"}, "positive integer"},
		{[]string{"migrate", "down", "2", "--all"}, "not both"},
		{[]string{"migrate", "down", "1", "2"}, "accepts at most 1 arg"},
		{[]string{"migrate", "force"}, "accepts 1 arg"},
		{[]string{"migrate", "force", "--", "-2"}, "integer >= -1"},
		{[]string{"migrate", "force", "abc"}, "integer >= -1"},
		{[]string{"migrate", "up", "extra"}, "unknown command"},
	}
	for _, tc := range cases {
		if _, err := run(t, env, tc.args...); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%v = %v, want %q", tc.args, err, tc.wantErr)
		}
	}
}

func TestDownSteps(t *testing.T) {
	cases := []struct {
		args []string
		all  bool
		want int
	}{
		{nil, false, 1},
		{[]string{"3"}, false, 3},
		{nil, true, 0},
	}
	for _, tc := range cases {
		if got, err := downSteps(tc.args, tc.all); err != nil || got != tc.want {
			t.Errorf("downSteps(%v, %v) = %d, %v; want %d", tc.args, tc.all, got, err, tc.want)
		}
	}
}
