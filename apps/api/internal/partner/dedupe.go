package partner

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// Duplicate match kinds, in the order they are reported.
const (
	MatchPhone   = "phone"
	MatchTaxCode = "tax_code"
	MatchName    = "name"
)

// maxDuplicateWarnings caps the parties a single warning lists.
const maxDuplicateWarnings = 5

// DedupeKeys are the normalised keys two records of the same partner tend to share.
type DedupeKeys struct {
	PhoneKey string
	TaxCode  string
	NameKey  string
}

// DuplicateWarning is an existing party that looks like the one being saved. It is a warning,
// never a block: two shops may share a name, a family may share a phone.
type DuplicateWarning struct {
	PartyID    uuid.UUID
	Name       string
	Matches    []string
	IsCustomer bool
	IsSupplier bool
}

// dedupeCandidate is a party sharing at least one key.
type dedupeCandidate struct {
	ID         uuid.UUID `db:"id"`
	Name       string    `db:"name"`
	NameKey    string    `db:"name_key"`
	PhoneKey   *string   `db:"phone_key"`
	TaxCode    *string   `db:"tax_code"`
	IsCustomer bool      `db:"is_customer"`
	IsSupplier bool      `db:"is_supplier"`
}

// PartyDeduplicator finds existing parties that match a party by phone, tax code or name.
type PartyDeduplicator struct{}

// Warnings turns the candidates into warnings: which keys match, strongest matches first
// (phone and tax code identify a partner better than a name), at most maxDuplicateWarnings.
func (PartyDeduplicator) Warnings(keys DedupeKeys, candidates []dedupeCandidate) []DuplicateWarning {
	type scored struct {
		w     DuplicateWarning
		score int
	}
	var out []scored
	for _, c := range candidates {
		var matches []string
		score := 0
		if keys.PhoneKey != "" && c.PhoneKey != nil && *c.PhoneKey == keys.PhoneKey {
			matches, score = append(matches, MatchPhone), score+4
		}
		if keys.TaxCode != "" && c.TaxCode != nil && *c.TaxCode == keys.TaxCode {
			matches, score = append(matches, MatchTaxCode), score+4
		}
		if keys.NameKey != "" && c.NameKey == keys.NameKey {
			matches, score = append(matches, MatchName), score+1
		}
		if len(matches) == 0 {
			continue
		}
		out = append(out, scored{score: score, w: DuplicateWarning{
			PartyID: c.ID, Name: c.Name, Matches: matches, IsCustomer: c.IsCustomer, IsSupplier: c.IsSupplier,
		}})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].w.PartyID.String() < out[j].w.PartyID.String()
	})
	if len(out) > maxDuplicateWarnings {
		out = out[:maxDuplicateWarnings]
	}
	warnings := make([]DuplicateWarning, len(out))
	for i, s := range out {
		warnings[i] = s.w
	}
	return warnings
}

// Check returns the warnings for keys among the tenant's parties other than self.
func (d PartyDeduplicator) Check(ctx context.Context, q db.Querier, self uuid.UUID, keys DedupeKeys) ([]DuplicateWarning, error) {
	if keys.PhoneKey == "" && keys.TaxCode == "" && keys.NameKey == "" {
		return nil, nil
	}
	var candidates []dedupeCandidate
	err := sqlx.SelectContext(ctx, q, &candidates, `
		SELECT p.id, p.name, p.name_key, p.phone_key, p.tax_code,
		       EXISTS (SELECT 1 FROM customer_profiles c WHERE c.party_id = p.id) AS is_customer,
		       EXISTS (SELECT 1 FROM supplier_profiles s WHERE s.party_id = p.id) AS is_supplier
		FROM parties p
		WHERE p.id <> $1
		  AND (($2 <> '' AND p.phone_key = $2) OR ($3 <> '' AND p.tax_code = $3) OR ($4 <> '' AND p.name_key = $4))
		ORDER BY p.id
		LIMIT 50`, self, keys.PhoneKey, keys.TaxCode, keys.NameKey)
	if err != nil {
		return nil, fmt.Errorf("partner: find duplicates: %w", err)
	}
	return d.Warnings(keys, candidates), nil
}

// dedupeKeysOf are the keys of a party.
func dedupeKeysOf(p Party) DedupeKeys {
	return DedupeKeys{PhoneKey: deref(p.PhoneKey), TaxCode: deref(p.TaxCode), NameKey: p.NameKey}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
