package cli

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/config"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// mapLookup is a config.Lookup over a fixed environment.
func mapLookup(env map[string]string) config.Lookup {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

// run executes the command tree with args and returns its output.
func run(t testing.TB, env map[string]string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCommand("test", &out, &out, mapLookup(env))
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestRunHTTPServerStopsOnContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runHTTPServer(ctx, ln, okHandler(), nil, discardLogger()) }()

	url := "http://" + ln.Addr().String() + "/healthz"
	if err := checkHealth(context.Background(), http.DefaultClient, url, 2*time.Second); err != nil {
		t.Fatalf("server not healthy: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runHTTPServer returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after context cancel")
	}
}

// A stream that only ends when told to must not hold the drain: onShutdown tells it.
func TestRunHTTPServerRunsOnShutdownBeforeDraining(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	stop, opened := make(chan struct{}), make(chan struct{})
	stream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		http.NewResponseController(w).Flush() //nolint:errcheck // the test reads the status line only
		close(opened)
		<-stop
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runHTTPServer(ctx, ln, stream, func() { close(stop) }, discardLogger()) }()

	go func() {
		res, err := http.Get("http://" + ln.Addr().String() + "/stream") //nolint:noctx // ends with the server
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not open")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runHTTPServer returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the drain waited for the open stream")
	}
}

func TestRunServersStopsAllWhenOneFails(t *testing.T) {
	lns, err := listen("127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// A closed listener makes its server fail at once; the other must shut down too.
	_ = lns[1].Close()
	done := make(chan error, 1)
	go func() {
		done <- runServers(context.Background(), discardLogger(),
			namedServer{name: "a", ln: lns[0], handler: okHandler()},
			namedServer{name: "b", ln: lns[1], handler: okHandler()})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("runServers returned nil, want the failing server's error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServers did not stop after one server failed")
	}
}

func TestListenClosesOpenedListenersOnFailure(t *testing.T) {
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	busy := first.Addr().String()
	defer func() { _ = first.Close() }()

	if _, err := listen("127.0.0.1:0", busy); err == nil || !strings.Contains(err.Error(), busy) {
		t.Fatalf("listen on a busy address = %v, want an error naming it", err)
	}
}

func TestServeAndWorkerReportMissingConfiguration(t *testing.T) {
	for _, cmd := range []string{"serve", "worker"} {
		_, err := run(t, map[string]string{}, cmd)
		if err == nil {
			t.Fatalf("%s without configuration succeeded", cmd)
		}
		for _, key := range []string{"PUBLIC_BASE_URL", "DATABASE_URL", "SMTP_HOST", "OBJECT_STORE_BUCKET"} {
			if !strings.Contains(err.Error(), key) {
				t.Errorf("%s error %q does not name %s", cmd, err, key)
			}
		}
	}
}

func TestWorkerRejectsNonPositiveIntervals(t *testing.T) {
	for _, flag := range []string{"--dispatch-interval=0s", "--purge-interval=-1h", "--shift-interval=0s"} {
		if _, err := run(t, nil, "worker", flag); err == nil || !strings.Contains(err.Error(), "positive") {
			t.Errorf("worker %s = %v, want interval error", flag, err)
		}
	}
}

func TestMigrateUpOnStartWithoutMigrationsOnlyWarns(t *testing.T) {
	setMigrations(t, nil)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	if err := migrateUp("postgres://unused", logger); err != nil {
		t.Fatalf("migrateUp = %v, want nil", err)
	}
	if !strings.Contains(logs.String(), "no migrations") {
		t.Errorf("missing warning: %s", logs.String())
	}

	setMigrations(t, fstest.MapFS{"000001_x.up.sql": {Data: []byte("SELECT 1")}})
	if err := migrateUp("not a url", logger); err == nil {
		t.Error("migrateUp with an invalid URL succeeded")
	}
}

func TestEveryRunsImmediatelyAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	done := make(chan struct{})
	go func() {
		every(ctx, time.Millisecond, func(context.Context) {
			runs++
			if runs == 3 {
				cancel()
			}
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("every did not stop after cancel")
	}
	if runs < 3 {
		t.Errorf("runs = %d, want >= 3", runs)
	}
}

func TestRequiredFlags(t *testing.T) {
	cases := [][]string{
		{"tenant", "create", "--name", "A"},
		{"seed", "demo"},
		{"maintenance", "revoke-sessions"},
		{"ledger", "rebuild"},
	}
	for _, args := range cases {
		if _, err := run(t, nil, args...); err == nil || !strings.Contains(err.Error(), "required flag") {
			t.Errorf("%v = %v, want required flag error", args, err)
		}
	}
}

func TestAccountCommandsReportMissingConfiguration(t *testing.T) {
	cases := [][]string{
		{"tenant", "create", "--name", "A", "--owner-email", "a@example.test"},
		{"seed", "demo", "--owner-email", "demo@example.test"},
		{"maintenance", "revoke-sessions", "--tenant", "store-a"},
	}
	for _, args := range cases {
		_, err := run(t, map[string]string{}, args...)
		if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") || !strings.Contains(err.Error(), "SMTP_HOST is required") {
			t.Errorf("%v = %v, want configuration error", args, err)
		}
	}
	if _, err := run(t, map[string]string{}, "maintenance", "revoke-sessions", "--tenant", "x", "--reason", " "); err == nil ||
		!strings.Contains(err.Error(), "--reason") {
		t.Errorf("blank reason = %v", err)
	}
}

func TestPurgeIdempotencyRequiresDatabaseURL(t *testing.T) {
	if _, err := run(t, map[string]string{}, "maintenance", "purge-idempotency"); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("purge-idempotency = %v, want DATABASE_URL error", err)
	}
}

// setMigrations swaps the embedded migrations for the duration of the test.
func setMigrations(t *testing.T, fsys fs.FS) {
	t.Helper()
	prev := migrationsFS
	migrationsFS = fsys
	t.Cleanup(func() { migrationsFS = prev })
}
