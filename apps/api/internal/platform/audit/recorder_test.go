package audit

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRedact(t *testing.T) {
	got, err := Redact(map[string]any{
		"name":     "Kho A",
		"password": "hunter2",
		"nested":   []any{map[string]any{"api_token": "abc", "qty": "1.50"}},
		"smtp":     map[string]any{"secret": "s", "host": "mail"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, leaked := range []string{"hunter2", `"abc"`, `"s"`} {
		if strings.Contains(s, leaked) {
			t.Errorf("redacted JSON leaks %s: %s", leaked, s)
		}
	}
	for _, kept := range []string{"Kho A", "1.50", "mail"} {
		if !strings.Contains(s, kept) {
			t.Errorf("redacted JSON lost %s: %s", kept, s)
		}
	}
	if b, err := Redact(nil); err != nil || b != nil {
		t.Errorf("Redact(nil) = %s, %v", b, err)
	}
	var nilMap map[string]any
	if b, err := Redact(nilMap); err != nil || b != nil {
		t.Errorf("Redact(nil map) = %s, %v", b, err)
	}
	if _, err := Redact(make(chan int)); err == nil {
		t.Error("unmarshalable value accepted")
	}
}

func TestActorValidate(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	cases := []struct {
		name  string
		actor Actor
		ok    bool
	}{
		{"owner", Actor{TenantID: tenant, Type: ActorOwner, ID: user, Source: SourceWeb}, true},
		{"system", Actor{TenantID: tenant, Type: ActorSystem, Source: SourceWorker}, true},
		{"no tenant", Actor{Type: ActorSystem, Source: SourceWorker}, false},
		{"owner without id", Actor{TenantID: tenant, Type: ActorOwner, Source: SourceWeb}, false},
		{"unknown type", Actor{TenantID: tenant, Type: "robot", Source: SourceWeb}, false},
		{"unknown source", Actor{TenantID: tenant, Type: ActorSystem, Source: "fax"}, false},
	}
	for _, tc := range cases {
		if err := tc.actor.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestNullIP(t *testing.T) {
	if nullIP("not-an-ip") != nil {
		t.Error("invalid IP kept")
	}
	if got := nullIP("::ffff:10.0.0.1"); got != "10.0.0.1" {
		t.Errorf("mapped IPv4 = %v", got)
	}
}
