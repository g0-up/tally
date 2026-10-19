// Package ids generates and parses identifiers. Every primary key is a UUIDv7 created in Go;
// the database has no id defaults.
package ids

import (
	"fmt"

	"github.com/google/uuid"
)

// New returns a new time-ordered UUIDv7.
func New() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

// Parse accepts only the canonical 36-character form, so ids in URLs have one spelling.
func Parse(s string) (uuid.UUID, error) {
	if len(s) != 36 {
		return uuid.Nil, fmt.Errorf("invalid id %q: want canonical UUID", s)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid id %q: %w", s, err)
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("invalid id: nil UUID")
	}
	return id, nil
}

// crockford is the Crockford base32 alphabet: no I, L, O or U, so a code read aloud or typed by
// hand has one spelling.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Crockford renders the low 5*n bits of id (its random tail for a UUIDv7) as n Crockford base32
// characters, for human-facing references such as "SV-20261018-7K2QXM". n is clamped to 1..12.
func Crockford(id uuid.UUID, n int) string {
	n = min(max(n, 1), 12)
	var v uint64
	for _, b := range id[8:] {
		v = v<<8 | uint64(b)
	}
	out := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		out[i] = crockford[v&31]
		v >>= 5
	}
	return string(out)
}
