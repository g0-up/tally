package cli

import (
	"log/slog"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/featureflag"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/support"
)

// controlStack is the platform control side the server adds to the account stack: admin
// sign-in, the admin API, support, incidents and feedback.
type controlStack struct {
	auth      *admin.AuthService
	admin     *admin.Handler
	support   *support.Service
	incidents *admin.Incidents
	feedback  *admin.FeedbackBox
}

func newControlStack(database *db.DB, accounts accountStack, secrets config.Secrets, logger *slog.Logger) (controlStack, error) {
	keys, err := admin.NewKeys(secrets.SessionHashKey, secrets.EncryptionKey)
	if err != nil {
		return controlStack{}, err
	}
	hasher, err := authn.NewHasher(authn.DefaultParams)
	if err != nil {
		return controlStack{}, err
	}
	auth, err := admin.NewAuthService(admin.AuthDeps{DB: database, Clock: accounts.clock, Hasher: hasher, Keys: keys, Logger: logger})
	if err != nil {
		return controlStack{}, err
	}
	sa := accounts.domains.SaaS
	out := controlStack{
		auth: auth,
		support: support.NewService(support.Deps{
			DB: database, Clock: accounts.clock, Audit: accounts.audit, Consent: accounts.identity, SLA: sa,
		}),
		incidents: admin.NewIncidents(database, accounts.clock),
		feedback:  admin.NewFeedbackBox(database, accounts.clock),
	}
	out.admin = admin.NewHandler(admin.HandlerDeps{
		Auth: auth, Directory: admin.NewDirectory(database, sa), SaaS: sa,
		Flags:     featureflag.NewWriter(database, accounts.clock, accounts.domains.Flags),
		Incidents: out.incidents, Partners: admin.NewPartners(database, accounts.clock), Feedback: out.feedback, Logger: logger,
	})
	return out, nil
}
