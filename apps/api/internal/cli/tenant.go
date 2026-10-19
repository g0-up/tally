package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
)

func newTenantCommand(lookup config.Lookup) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant",
		Short: "Manage tenants (stores)",
	}
	var name, slug, ownerEmail, ownerName string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a tenant with its owner and default data, and mail the owner a verification link",
		Long: "Create a tenant with its single store and warehouse, settings and default data, and its owner\n" +
			"pending verification. The owner chooses a password from the mailed verification link, which is\n" +
			"also printed outside production.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withAccounts(cmd, lookup, func(ctx context.Context, env accountEnv) error {
				if slug == "" {
					suggested, err := suggestSlug(ctx, env.stack.identity, name)
					if err != nil {
						return err
					}
					slug = suggested
				}
				created, err := env.stack.identity.CreateTenantWithOwner(ctx, identity.OwnerSpec{
					Tenant: identity.TenantSpec{ID: ids.New(), Slug: slug, DisplayName: name},
					Email:  ownerEmail, FullName: ownerNameOr(ownerName, name), Source: audit.SourceCLI,
				})
				if err != nil {
					return err
				}
				return printCreated(cmd.OutOrStdout(), created, env.production)
			})
		},
	}
	create.Flags().StringVar(&name, "name", "", "store name")
	create.Flags().StringVar(&slug, "slug", "", "store address (default: suggested from the store name)")
	create.Flags().StringVar(&ownerEmail, "owner-email", "", "owner e-mail address")
	create.Flags().StringVar(&ownerName, "owner-name", "", "owner full name (default: the store name)")
	_ = create.MarkFlagRequired("name")
	_ = create.MarkFlagRequired("owner-email")
	cmd.AddCommand(create)
	cmd.AddCommand(newTenantRestoreCommands(lookup)...)
	return cmd
}

// accountEnv is what the tenant and account commands work with.
type accountEnv struct {
	stack      accountStack
	database   *db.DB
	production bool
}

// withAccounts loads the provisioning configuration, opens the database, builds the identity
// service and runs fn; queued mails are delivered before it returns.
func withAccounts(cmd *cobra.Command, lookup config.Lookup, fn func(ctx context.Context, env accountEnv) error) error {
	cfg, err := config.LoadProvision(lookup)
	if err != nil {
		return err
	}
	logger, err := logging.New(cmd.ErrOrStderr(), cfg.LogLevel)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	database, err := db.Open(ctx, cfg.Database.URL, db.Options{
		MaxConns:         2,
		StatementTimeout: cfg.Database.StatementTimeout,
		ApplicationName:  "tally-cli",
	})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	stack, err := newAccountStack(database, accountConfig{
		HTTP: cfg.HTTP, Secrets: cfg.Secrets, Mail: cfg.Mail, Location: cfg.Location,
	}, logger, nil)
	if err != nil {
		return err
	}
	defer stack.identity.WaitMail()
	return fn(ctx, accountEnv{stack: stack, database: database, production: cfg.IsProduction()})
}

// suggestSlug derives a free store address from the store name.
func suggestSlug(ctx context.Context, svc *identity.Service, name string) (string, error) {
	check, err := svc.CheckSlug(ctx, name)
	if err != nil {
		return "", err
	}
	if check.Available {
		return strings.TrimSpace(name), nil
	}
	if check.Suggestion == "" {
		return "", errors.New("no free store address can be derived from the name; pass --slug")
	}
	return check.Suggestion, nil
}

func ownerNameOr(ownerName, storeName string) string {
	if strings.TrimSpace(ownerName) != "" {
		return ownerName
	}
	return storeName
}

// printCreated reports a new tenant. The verification link is a credential: it is printed only
// outside production, where the owner gets it by mail anyway.
func printCreated(w io.Writer, c identity.CreatedTenant, production bool) error {
	_, err := fmt.Fprintf(w, "created tenant %s (%s) id=%s owner=%s\n", c.Tenant.Slug, c.Tenant.DisplayName, c.Tenant.ID, c.Owner.Email)
	if err != nil || production {
		return err
	}
	_, err = fmt.Fprintf(w, "verification link (one-time, development only): %s\n", c.VerifyURL)
	return err
}
