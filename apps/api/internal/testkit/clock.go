// Package testkit starts the throwaway PostgreSQL used by integration tests and offers small
// fixtures and HTTP helpers. The container (postgres:16-alpine) is created once per test binary,
// gets the production roles from infra/postgres, has the schema applied as tally_owner, and
// hands tests a pool connected as tally_app, so RLS and grants behave as in production.
package testkit

import (
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
)

// Epoch is the instant test clocks start at: 2026-03-15 03:00 UTC (10:00 in Asia/Ho_Chi_Minh).
var Epoch = time.Date(2026, time.March, 15, 3, 0, 0, 0, time.UTC)

// Clock returns a fixed clock at Epoch.
func Clock() *clock.Fixed { return clock.NewFixed(Epoch) }
