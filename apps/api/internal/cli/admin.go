package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
)

func newAdminCommand(lookup config.Lookup) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage platform operator accounts",
	}
	var email, name string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a platform admin and print its TOTP enrolment once",
		Long: "Creates an active platform admin. The password is read from the first line of standard input,\n" +
			"so it stays out of the shell history. Sign-in always asks for a TOTP code: the enrolment\n" +
			"secret and otpauth:// link are printed once, here, and cannot be shown again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			password, err := readPassword(cmd.InOrStdin())
			if err != nil {
				return err
			}
			cfg, err := config.LoadProvision(lookup)
			if err != nil {
				return err
			}
			logger, err := logging.New(cmd.ErrOrStderr(), cfg.LogLevel)
			if err != nil {
				return err
			}
			database, err := db.Open(cmd.Context(), cfg.Database.URL, db.Options{
				MaxConns: 2, StatementTimeout: cfg.Database.StatementTimeout, ApplicationName: "tally-cli",
			})
			if err != nil {
				return err
			}
			defer func() { _ = database.Close() }()
			keys, err := admin.NewKeys(cfg.Secrets.SessionHashKey, cfg.Secrets.EncryptionKey)
			if err != nil {
				return err
			}
			hasher, err := authn.NewHasher(authn.DefaultParams)
			if err != nil {
				return err
			}
			auth, err := admin.NewAuthService(admin.AuthDeps{DB: database, Clock: clock.System{}, Hasher: hasher, Keys: keys, Logger: logger})
			if err != nil {
				return err
			}
			e, err := auth.CreateAdmin(cmd.Context(), admin.NewAdmin{Email: email, FullName: name, Password: password})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"created platform admin %s id=%s\nTOTP secret (shown once): %s\nTOTP enrolment link (shown once): %s\n",
				e.Email, e.AdminID, e.Secret, e.URI)
			return err
		},
	}
	create.Flags().StringVar(&email, "email", "", "admin e-mail address")
	create.Flags().StringVar(&name, "name", "", "admin full name")
	_ = create.MarkFlagRequired("email")
	_ = create.MarkFlagRequired("name")
	cmd.AddCommand(create)
	return cmd
}

// readPassword reads the first line of r, without its line ending.
func readPassword(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password from standard input: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", errors.New("pass the admin password on standard input (first line)")
	}
	return password, nil
}
