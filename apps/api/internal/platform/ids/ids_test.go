package ids

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewIsVersion7AndOrdered(t *testing.T) {
	prev := New()
	for i := 0; i < 1000; i++ {
		id := New()
		if id.Version() != 7 {
			t.Fatalf("version = %d, want 7", id.Version())
		}
		if bytes.Compare(prev[:], id[:]) >= 0 {
			t.Fatalf("ids not increasing: %s then %s", prev, id)
		}
		prev = id
	}
}

func TestParse(t *testing.T) {
	good := New().String()
	if _, err := Parse(good); err != nil {
		t.Fatalf("Parse(%q) = %v", good, err)
	}
	for _, bad := range []string{"", "abc", "{" + good + "}", strings.ReplaceAll(good, "-", ""), "urn:uuid:" + good,
		"00000000-0000-0000-0000-000000000000", "zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestCrockford(t *testing.T) {
	id := uuid.MustParse("00000000-0000-7000-8000-00000000001f")
	if got := Crockford(id, 6); got != "00000Z" {
		t.Fatalf("Crockford = %q, want 00000Z", got)
	}
	for i := 0; i < 200; i++ {
		got := Crockford(New(), 6)
		if len(got) != 6 || strings.ContainsAny(got, "ILOU") {
			t.Fatalf("Crockford = %q, want 6 chars without I, L, O, U", got)
		}
	}
	if got := Crockford(id, 99); len(got) != 12 {
		t.Fatalf("Crockford clamps n to 12, got %q", got)
	}
}
