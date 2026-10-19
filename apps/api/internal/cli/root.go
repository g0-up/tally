// Package cli wires the cobra command tree of the tally binary.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/platform/config"
)

// Execute runs the command tree and returns the process exit code.
// The context is cancelled on SIGINT/SIGTERM so long-running commands shut down gracefully.
func Execute(version string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCommand(version, os.Stdout, os.Stderr, config.OSLookup)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// newRootCommand builds the tree. lookup reads the environment (a map in tests).
func newRootCommand(version string, stdout, stderr io.Writer, lookup config.Lookup) *cobra.Command {
	root := &cobra.Command{
		Use:           "tally",
		Short:         "Tally API server, background worker and operator CLI",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(
		newServeCommand(version, lookup),
		newWorkerCommand(version, lookup),
		newMigrateCommand(lookup),
		newSeedCommand(lookup),
		newTenantCommand(lookup),
		newLedgerCommand(lookup),
		newMaintenanceCommand(lookup),
		newAdminCommand(lookup),
		newHealthcheckCommand(),
	)
	return root
}
