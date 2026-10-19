package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// standaloneDemoName is the store name of the development demo tenant.
const standaloneDemoName = "Cửa hàng demo"

func newSeedCommand(lookup config.Lookup) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Load seed data",
	}
	cmd.AddCommand(newSeedDevCommand(lookup))

	var parent, ownerEmail, ownerName string
	demo := &cobra.Command{
		Use:   "demo",
		Short: "Create a demo (sandbox) tenant with sample data",
		Long: "Create a sandbox tenant: \"<parent>-demo\" for the store given with --parent, or \"demo\" without\n" +
			"one (development). Its owner, who needs an e-mail address of their own, chooses a password from\n" +
			"the mailed verification link.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withAccounts(cmd, lookup, func(ctx context.Context, env accountEnv) error {
				spec := identity.TenantSpec{ID: ids.New(), Slug: org.StandaloneSandboxSlug, DisplayName: standaloneDemoName, IsSandbox: true}
				if parent != "" {
					p, err := parentTenant(ctx, env, parent)
					if err != nil {
						return err
					}
					spec.Slug, spec.DisplayName = p.Slug+org.SandboxSuffix, p.DisplayName+" (demo)"
					spec.SandboxOf, spec.ParentSlug = &p.ID, p.Slug
				}
				created, err := env.stack.identity.CreateTenantWithOwner(ctx, identity.OwnerSpec{
					Tenant: spec, Email: ownerEmail, FullName: ownerNameOr(ownerName, spec.DisplayName), Source: audit.SourceCLI,
				})
				if err != nil {
					return err
				}
				return printCreated(cmd.OutOrStdout(), created, env.production)
			})
		},
	}
	demo.Flags().StringVar(&parent, "parent", "", "slug of the real store the demo belongs to (default: standalone \"demo\")")
	demo.Flags().StringVar(&ownerEmail, "owner-email", "", "demo owner e-mail address (not used by another account)")
	demo.Flags().StringVar(&ownerName, "owner-name", "", "demo owner full name (default: the demo store name)")
	_ = demo.MarkFlagRequired("owner-email")
	cmd.AddCommand(demo)
	cmd.AddCommand(newSeedLoadCommand(lookup))
	return cmd
}

// parentTenant reads the real store a demo tenant belongs to.
func parentTenant(ctx context.Context, env accountEnv, slug string) (org.Tenant, error) {
	id, found, err := env.stack.identity.ResolveSlug(ctx, slug)
	if err != nil {
		return org.Tenant{}, err
	}
	if !found {
		return org.Tenant{}, fmt.Errorf("no tenant has the address %q", slug)
	}
	svc := org.NewService(org.Deps{DB: env.database, Clock: env.stack.clock, Audit: env.stack.audit, Provisioner: env.stack.provisioner})
	t, err := svc.Tenant(ctx, id)
	if err != nil {
		return org.Tenant{}, err
	}
	if t.IsSandbox {
		return org.Tenant{}, fmt.Errorf("%q is itself a demo tenant", slug)
	}
	return t, nil
}
