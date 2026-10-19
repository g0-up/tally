package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/migrations"
)

// migrationsFS holds the *.up.sql / *.down.sql files embedded in the binary (replaced in tests).
var migrationsFS fs.FS = migrations.FS

// maxBackupAge is how recent the --backup-file must be for a guarded schema change.
const maxBackupAge = time.Hour

// pgDumpMagic opens every pg_dump custom-format archive (pg_dump -Fc, what make db-backup writes).
var pgDumpMagic = []byte("PGDMP")

// backupClock is the wall clock of the backup guard (replaced in tests).
var backupClock = time.Now

func newMigrateCommand(lookup config.Lookup) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply or inspect schema migrations (connects as MIGRATE_DATABASE_URL)",
		Long: "Apply or inspect schema migrations as the schema owner (MIGRATE_DATABASE_URL).\n" +
			"up, down and force require --backup-file unless APP_ENV is explicitly development or test\n" +
			"(an unset APP_ENV does not waive it): a pg_dump custom-format archive written less than an\n" +
			"hour ago (make db-backup / make prod-migrate).",
	}
	cmd.AddCommand(
		newMigrateUpCommand(lookup),
		newMigrateDownCommand(lookup),
		newMigrateVersionCommand(lookup),
		newMigrateForceCommand(lookup),
	)
	return cmd
}

func newMigrateUpCommand(lookup config.Lookup) *cobra.Command {
	var backupFile string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Apply every pending migration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withMigrator(lookup, true, backupFile, func(m *db.Migrator) error {
				if err := m.Up(); err != nil {
					return err
				}
				return printVersion(cmd, m)
			})
		},
	}
	addBackupFlag(cmd, &backupFile)
	return cmd
}

func newMigrateDownCommand(lookup config.Lookup) *cobra.Command {
	var (
		backupFile string
		all        bool
	)
	cmd := &cobra.Command{
		Use:   "down [N]",
		Short: "Roll back the last N migrations (default 1), or every migration with --all",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			steps, err := downSteps(args, all)
			if err != nil {
				return err
			}
			return withMigrator(lookup, true, backupFile, func(m *db.Migrator) error {
				if err := m.Down(steps); err != nil {
					return err
				}
				return printVersion(cmd, m)
			})
		},
	}
	addBackupFlag(cmd, &backupFile)
	cmd.Flags().BoolVar(&all, "all", false, "roll back every migration")
	return cmd
}

func newMigrateVersionCommand(lookup config.Lookup) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the applied migration version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withMigrator(lookup, false, "", func(m *db.Migrator) error { return printVersion(cmd, m) })
		},
	}
}

func newMigrateForceCommand(lookup config.Lookup) *cobra.Command {
	var backupFile string
	cmd := &cobra.Command{
		Use:   "force VERSION",
		Short: "Mark VERSION as applied and clean without running it (repair after a failed migration; -- -1 means none)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version, err := strconv.Atoi(args[0])
			if err != nil || version < -1 {
				return fmt.Errorf("VERSION must be an integer >= -1, got %q", args[0])
			}
			return withMigrator(lookup, true, backupFile, func(m *db.Migrator) error {
				if err := m.Force(version); err != nil {
					return err
				}
				return printVersion(cmd, m)
			})
		},
	}
	addBackupFlag(cmd, &backupFile)
	return cmd
}

func addBackupFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "backup-file", "",
		"pg_dump -Fc archive taken right before this change (required unless APP_ENV is development or test)")
}

// downSteps returns the number of migrations to roll back; 0 means all.
func downSteps(args []string, all bool) (int, error) {
	if all {
		if len(args) > 0 {
			return 0, errors.New("use either N or --all, not both")
		}
		return 0, nil
	}
	if len(args) == 0 {
		return 1, nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("the number of migrations must be a positive integer, got %q", args[0])
	}
	return n, nil
}

// withMigrator checks the backup guard (for mutating commands) before reading the connection
// string or connecting, then runs fn with an open migrator.
func withMigrator(lookup config.Lookup, mutating bool, backupFile string, fn func(*db.Migrator) error) error {
	base, err := config.LoadBase(lookup)
	if err != nil {
		return err
	}
	if mutating && backupRequired(lookup, base) {
		if err := checkBackup(backupFile, backupClock()); err != nil {
			return err
		}
	}
	cfg, err := config.LoadMigrate(lookup)
	if err != nil {
		return err
	}
	m, err := db.NewMigrator(cfg.URL, migrationsFS)
	if err != nil {
		return err
	}
	return errors.Join(fn(m), m.Close())
}

// backupRequired reports whether a schema change needs --backup-file. Only an APP_ENV set
// explicitly to development or test waives it: the migrator cannot tell which database
// MIGRATE_DATABASE_URL points at.
func backupRequired(lookup config.Lookup, base config.Base) bool {
	return !explicitDevelopmentOrTest(lookup, base)
}

// explicitDevelopmentOrTest reports whether APP_ENV is set, explicitly, to development or test.
// Most commands read an unset APP_ENV as development, but a command that would harm a production
// database cannot tell which database it reaches, so it treats a shell that forgot APP_ENV like
// production.
func explicitDevelopmentOrTest(lookup config.Lookup, base config.Base) bool {
	if lookup == nil {
		lookup = config.OSLookup
	}
	raw, ok := lookup("APP_ENV")
	if !ok || strings.TrimSpace(raw) == "" {
		return false
	}
	return base.Env == config.Development || base.Env == config.Test
}

// checkBackup refuses a schema change unless backupFile is a pg_dump custom-format archive (it
// starts with the PGDMP signature) modified within maxBackupAge before at.
func checkBackup(backupFile string, at time.Time) error {
	if backupFile == "" {
		return errors.New("refusing to change the schema without --backup-file: take a backup first " +
			"(make prod-migrate does both), or set APP_ENV=development or APP_ENV=test for a local database")
	}
	info, err := os.Stat(backupFile)
	if err != nil {
		return fmt.Errorf("--backup-file %s: %w", backupFile, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("--backup-file %s is not a non-empty regular file", backupFile)
	}
	if err := checkDumpSignature(backupFile); err != nil {
		return err
	}
	if age := at.Sub(info.ModTime()); age > maxBackupAge {
		return fmt.Errorf("--backup-file %s was written %s ago; take a fresh backup (at most %s old)",
			backupFile, age.Truncate(time.Second), maxBackupAge)
	}
	return nil
}

// checkDumpSignature verifies that path starts with the pg_dump custom-format signature, so a
// truncated, plain-SQL or unrelated file is not accepted as the backup.
func checkDumpSignature(path string) (err error) {
	f, err := os.Open(filepath.Clean(path)) // operator-supplied flag; reading it is the point
	if err != nil {
		return fmt.Errorf("--backup-file %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	head := make([]byte, len(pgDumpMagic))
	_, err = io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("--backup-file %s: %w", path, err)
	}
	if err != nil || !bytes.Equal(head, pgDumpMagic) {
		return fmt.Errorf("--backup-file %s is not a pg_dump custom-format archive (make db-backup writes one)", path)
	}
	return nil
}

func printVersion(cmd *cobra.Command, m *db.Migrator) error {
	version, dirty, err := m.Version()
	if err != nil {
		return err
	}
	state := "clean"
	if dirty {
		state = "dirty"
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "schema version %d (%s)\n", version, state)
	return err
}
