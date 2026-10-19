package org

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// Slug check reasons returned by GET /signup/slug-check.
const (
	SlugInvalid  = "invalid"
	SlugReserved = "reserved"
	SlugTaken    = "taken"
)

// SandboxSuffix ends every demo tenant slug; real stores cannot use it.
const SandboxSuffix = "-demo"

// StandaloneSandboxSlug is the slug of the development demo tenant without a parent.
const StandaloneSandboxSlug = "demo"

// slugFormat is the store address rule: 3–30 lowercase letters, digits and inner hyphens.
var slugFormat = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,28}[a-z0-9]$`)

// builtinReservedSlugs are labels that belong to the platform or are confusing as a store
// address; RESERVED_SLUGS adds the labels other services use on the same zone.
var builtinReservedSlugs = []string{
	"www", "app", "api", "admin", "auth", "login", "signup", "account", "billing", "pay", "mail", "smtp",
	"imap", "mx", "ns1", "ns2", "ftp", "static", "assets", "cdn", "img", "files", "status", "help",
	"support", "docs", "blog", "news", "dev", "staging", "test", "e2e", "tally",
}

// SlugPolicy decides which store addresses can be issued.
type SlugPolicy struct {
	reserved map[string]bool
}

// NewSlugPolicy returns the policy with the built-in reserved labels plus extra (RESERVED_SLUGS).
func NewSlugPolicy(extra []string) *SlugPolicy {
	p := &SlugPolicy{reserved: map[string]bool{}}
	for _, s := range builtinReservedSlugs {
		p.reserved[s] = true
	}
	for _, s := range extra {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			p.reserved[s] = true
		}
	}
	return p
}

// Reason returns "" when slug may be issued to a real store, otherwise SlugInvalid or
// SlugReserved. Availability is not checked.
func (p *SlugPolicy) Reason(slug string) string {
	if !slugFormat.MatchString(slug) || strings.Contains(slug, "--") || strings.HasSuffix(slug, SandboxSuffix) {
		// "--" also excludes punycode labels (xn--); "-demo" is kept for sandbox tenants.
		return SlugInvalid
	}
	if p.reserved[slug] {
		return SlugReserved
	}
	return ""
}

// Validate returns a 422 error unless slug may be issued to a real store.
func (p *SlugPolicy) Validate(slug string) error {
	switch p.Reason(slug) {
	case SlugInvalid:
		return apperr.NewValidation("slug_invalid",
			"The store address must be 3–30 lowercase letters, digits or single hyphens", map[string]string{"slug": SlugInvalid})
	case SlugReserved:
		return apperr.NewValidation("slug_reserved", "This store address is reserved", map[string]string{"slug": SlugReserved})
	}
	return nil
}

// ValidateSandbox checks the slug of a demo tenant: "<parent>-demo", or "demo" without a parent.
func (p *SlugPolicy) ValidateSandbox(slug, parentSlug string) error {
	want := StandaloneSandboxSlug
	if parentSlug != "" {
		want = parentSlug + SandboxSuffix
	}
	if slug != want {
		return apperr.NewValidation("slug_invalid", "A demo store address must be "+want, map[string]string{"slug": SlugInvalid})
	}
	return nil
}

// SlugCheck is the answer of the live store address check.
type SlugCheck struct {
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Check reports whether slug is free; when it is not, it suggests a free address derived from it
// (a store name typed into the field is turned into an address first).
func (p *SlugPolicy) Check(ctx context.Context, q db.Querier, slug string) (SlugCheck, error) {
	slug = strings.TrimSpace(slug)
	reason := p.Reason(slug)
	if reason == "" {
		taken, err := takenSlugs(ctx, q, []string{slug})
		if err != nil {
			return SlugCheck{}, err
		}
		if !taken[slug] {
			return SlugCheck{Available: true}, nil
		}
		reason = SlugTaken
	}
	suggestion, err := p.Suggest(ctx, q, slug)
	if err != nil {
		return SlugCheck{}, err
	}
	return SlugCheck{Reason: reason, Suggestion: suggestion}, nil
}

// maxSuggestionTries bounds the numbered candidates tried by Suggest.
const maxSuggestionTries = 50

// Suggest returns a free, valid store address derived from name (a store name or a slug):
// the slugified name itself, then name-2, name-3 …; "" when none of the candidates is free.
func (p *SlugPolicy) Suggest(ctx context.Context, q db.Querier, name string) (string, error) {
	base := strings.TrimSuffix(textnorm.Slug(name), SandboxSuffix)
	if base == "" {
		base = "cua-hang"
	}
	candidates := make([]string, 0, maxSuggestionTries+1)
	if p.Reason(base) == "" {
		candidates = append(candidates, base)
	}
	for n := 2; len(candidates) <= maxSuggestionTries; n++ {
		suffix := "-" + strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > textnorm.SlugMaxLen {
			stem = strings.TrimRight(stem[:textnorm.SlugMaxLen-len(suffix)], "-")
		}
		if c := stem + suffix; p.Reason(c) == "" {
			candidates = append(candidates, c)
		}
		if n > 10*maxSuggestionTries {
			break
		}
	}
	taken, err := takenSlugs(ctx, q, candidates)
	if err != nil {
		return "", err
	}
	for _, c := range candidates {
		if !taken[c] {
			return c, nil
		}
	}
	return "", nil
}

// takenSlugs returns which of slugs belong to a tenant or to a purged tenant (never reissued).
func takenSlugs(ctx context.Context, q db.Querier, slugs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(slugs) == 0 {
		return out, nil
	}
	rows, err := q.QueryxContext(ctx, `
		SELECT slug FROM tenants WHERE slug = ANY($1)
		UNION
		SELECT slug FROM retired_tenant_slugs WHERE slug = ANY($1)`, slugs)
	if err != nil {
		return nil, fmt.Errorf("org: read taken slugs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("org: scan taken slug: %w", err)
		}
		out[s] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("org: read taken slugs: %w", err)
	}
	return out, nil
}

// SlugTakenError is the 422 answered when a slug is already in use.
func SlugTakenError() error {
	return apperr.NewValidation("slug_taken", "This store address is already in use", map[string]string{"slug": SlugTaken})
}
