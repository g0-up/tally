package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultHealthcheckURL     = "http://127.0.0.1:8080/healthz"
	defaultHealthcheckTimeout = 3 * time.Second
)

// newHealthcheckCommand probes the local API. The distroless image has no curl or
// wget, so container healthchecks run `tally healthcheck` instead.
func newHealthcheckCommand() *cobra.Command {
	var (
		probeURL string
		timeout  time.Duration
	)
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 when the local API answers 200 on its liveness endpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return checkHealth(cmd.Context(), http.DefaultClient, probeURL, timeout)
		},
	}
	cmd.Flags().StringVar(&probeURL, "url", defaultHealthcheckURL, "liveness URL to probe")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultHealthcheckTimeout, "maximum time to wait for the response")
	return cmd
}

func checkHealth(ctx context.Context, client *http.Client, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build healthcheck request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a bounded amount so the connection can be reused; the body content is irrelevant.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck %s: unexpected status %d", url, resp.StatusCode)
	}
	return nil
}
