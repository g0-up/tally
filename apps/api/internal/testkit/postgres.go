//go:build integration

package testkit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/migrations"
)

// Image is the PostgreSQL image used in production and tests.
const Image = "postgres:16-alpine"

// Test-only credentials of the throwaway container.
//
//nolint:gosec // throwaway test container, never reachable outside the test host
const (
	superUser     = "postgres"
	superPassword = "postgres"
	dbName        = "tally"
	ownerPassword = "tally_owner_test"
	appPassword   = "tally_app_test"
	roPassword    = "tally_readonly_test"
)

// Schema is the migration source applied to the container: the real embedded migrations.
var Schema fs.FS = migrations.FS

// SchemaVersion is the newest migration version in Schema, i.e. the version a fully migrated
// database reports.
func SchemaVersion(tb testing.TB) uint {
	tb.Helper()
	files, err := fs.Glob(Schema, "*.up.sql")
	if err != nil || len(files) == 0 {
		tb.Fatalf("testkit: no up migrations in Schema (err=%v)", err)
	}
	var latest uint
	for _, f := range files {
		prefix, _, ok := strings.Cut(f, "_")
		if !ok {
			tb.Fatalf("testkit: migration %q has no version prefix", f)
		}
		v, err := strconv.ParseUint(prefix, 10, 32)
		if err != nil {
			tb.Fatalf("testkit: migration %q: %v", f, err)
		}
		latest = max(latest, uint(v))
	}
	return latest
}

type pgEnv struct {
	ctr  *postgres.PostgresContainer
	host string
	port string
	app  *db.DB
}

var (
	startOnce sync.Once
	env       *pgEnv
	envErr    error
)

// Main runs the package tests and removes the container afterwards. Use it from TestMain:
//
//	func TestMain(m *testing.M) { testkit.Main(m) }
func Main(m *testing.M) {
	code := m.Run()
	if env != nil {
		_ = env.app.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := testcontainers.TerminateContainer(env.ctr, testcontainers.StopContext(ctx)); err != nil {
			fmt.Fprintf(os.Stderr, "testkit: terminate postgres: %v\n", err)
		}
		cancel()
	}
	os.Exit(code)
}

// DB returns the shared pool connected as tally_app, starting the container on first use.
// Tests isolate their data by creating their own tenants.
func DB(tb testing.TB) *db.DB {
	tb.Helper()
	return started(tb).app
}

// AppURL is the tally_app connection string.
func AppURL(tb testing.TB) string {
	tb.Helper()
	e := started(tb)
	return e.url("tally_app", appPassword)
}

// OwnerURL is the schema owner connection string (what MIGRATE_DATABASE_URL points at).
func OwnerURL(tb testing.TB) string {
	tb.Helper()
	e := started(tb)
	return e.url("tally_owner", ownerPassword)
}

// SuperURL is the superuser connection string, for checking that a command refuses a role that
// bypasses row level security.
func SuperURL(tb testing.TB) string {
	tb.Helper()
	e := started(tb)
	return e.url(superUser, superPassword)
}

// SuperDB opens a superuser connection that bypasses RLS, for assertions on what is really
// stored. Never use it to arrange data a test then reads through the application role.
func SuperDB(tb testing.TB) *sqlx.DB {
	tb.Helper()
	e := started(tb)
	x, err := sqlx.Open("pgx", e.url(superUser, superPassword))
	if err != nil {
		tb.Fatalf("testkit: open superuser connection: %v", err)
	}
	tb.Cleanup(func() { _ = x.Close() })
	return x
}

func (e *pgEnv) url(user, password string) string { return e.urlOf(user, password, dbName) }

func (e *pgEnv) urlOf(user, password, database string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(e.host, e.port),
		Path:     "/" + database,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

// Scratch is a database restored from a pg_dump of the test database, the way an operator
// restores a backup into a scratch server: same roles, schema, policies and rows as at the dump.
type Scratch struct {
	Name     string
	AppURL   string
	OwnerURL string
}

// RestoreScratch dumps the test database with pg_dump -Fc and restores the archive into a new
// database of the same server. The database is dropped when the test ends; close every pool
// opened on it before then.
func RestoreScratch(tb testing.TB) Scratch {
	tb.Helper()
	e := started(tb)
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		tb.Fatal(err)
	}
	name := "scratch_" + hex.EncodeToString(suffix)
	dump := "/tmp/" + name + ".dump"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	tb.Cleanup(func() {
		x, err := sqlx.Open("pgx", e.url(superUser, superPassword))
		if err != nil {
			tb.Errorf("testkit: drop scratch database: %v", err)
			return
		}
		defer func() { _ = x.Close() }()
		if _, err := x.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`); err != nil {
			tb.Errorf("testkit: drop scratch database: %v", err)
		}
	})
	for _, cmd := range [][]string{
		{"pg_dump", "-U", superUser, "-d", dbName, "-Fc", "-f", dump},
		{"createdb", "-U", superUser, name},
		{"pg_restore", "-U", superUser, "-d", name, "--exit-on-error", dump},
		{"rm", "-f", dump},
	} {
		code, out, err := e.ctr.Exec(ctx, cmd, tcexec.Multiplexed())
		if err != nil {
			tb.Fatalf("testkit: %s: %v", cmd[0], err)
		}
		if code != 0 {
			msg, _ := io.ReadAll(out)
			tb.Fatalf("testkit: %s exited %d: %s", cmd[0], code, msg)
		}
	}
	return Scratch{
		Name:     name,
		AppURL:   e.urlOf("tally_app", appPassword, name),
		OwnerURL: e.urlOf("tally_owner", ownerPassword, name),
	}
}

func started(tb testing.TB) *pgEnv {
	tb.Helper()
	startOnce.Do(func() { env, envErr = start() })
	if envErr != nil {
		tb.Fatalf("testkit: start postgres: %v", envErr)
	}
	return env
}

// repoRoot is four levels above this file (apps/api/internal/testkit).
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate testkit source")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func start() (*pgEnv, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	root, err := repoRoot()
	if err != nil {
		return nil, err
	}
	ctr, err := postgres.Run(ctx, Image,
		postgres.WithDatabase(dbName),
		postgres.WithUsername(superUser),
		postgres.WithPassword(superPassword),
		testcontainers.WithEnv(map[string]string{
			"TALLY_OWNER_PASSWORD":    ownerPassword,
			"TALLY_APP_PASSWORD":      appPassword,
			"TALLY_READONLY_PASSWORD": roPassword,
		}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath:      filepath.Join(root, "infra", "postgres", "roles.sql"),
			ContainerFilePath: "/etc/tally/roles.sql",
			FileMode:          0o644,
		}),
		postgres.WithInitScripts(filepath.Join(root, "infra", "postgres", "init", "01-roles.sh")),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		return nil, err
	}
	e := &pgEnv{ctr: ctr}
	fail := func(err error) (*pgEnv, error) {
		_ = testcontainers.TerminateContainer(ctr)
		return nil, err
	}
	if e.host, err = ctr.Host(ctx); err != nil {
		return fail(fmt.Errorf("container host: %w", err))
	}
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return fail(fmt.Errorf("container port: %w", err))
	}
	e.port = port.Port()

	m, err := db.NewMigrator(e.url("tally_owner", ownerPassword), Schema)
	if err != nil {
		return fail(err)
	}
	upErr := m.Up()
	closeErr := m.Close()
	if upErr != nil {
		return fail(upErr)
	}
	if closeErr != nil {
		return fail(fmt.Errorf("close migrator: %w", closeErr))
	}

	if e.app, err = db.Open(ctx, e.url("tally_app", appPassword), db.Options{MaxConns: 60, ApplicationName: "tally-test"}); err != nil {
		return fail(err)
	}
	return e, nil
}
