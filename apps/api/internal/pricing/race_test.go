//go:build race && integration

package pricing

// The race detector slows code several-fold, so wall-clock budgets are not checked under it.
func init() { raceEnabled = true }
