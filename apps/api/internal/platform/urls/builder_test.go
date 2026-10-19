package urls

import "testing"

func TestBuilderProduction(t *testing.T) {
	b, err := Parse("https://sidecup.app")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		b.Root("/signup"):                    "https://sidecup.app/signup",
		b.Root(""):                           "https://sidecup.app/",
		b.Tenant("demo", "/verify?token=a1"): "https://demo.sidecup.app/verify?token=a1",
		b.Tenant("tiem-son", "login"):        "https://tiem-son.sidecup.app/login",
		b.Admin("/admin/tenants"):            "https://admin.sidecup.app/admin/tenants",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if b.Host() != "sidecup.app" {
		t.Errorf("Host() = %q", b.Host())
	}
}

func TestBuilderDevelopmentKeepsSchemeAndPort(t *testing.T) {
	b, err := Parse("http://LocalHost:5173/")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b.Tenant("demo", "/pos"), "http://demo.localhost:5173/pos"; got != want {
		t.Errorf("Tenant = %q, want %q", got, want)
	}
	if got, want := b.Admin("/"), "http://admin.localhost:5173/"; got != want {
		t.Errorf("Admin = %q, want %q", got, want)
	}
	if got, want := b.Root("/find-store"), "http://localhost:5173/find-store"; got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
}

func TestBuilderRejectsBadBase(t *testing.T) {
	for _, raw := range []string{"", "ftp://x.app", "https://", "https://x.app/app", "https://u:p@x.app", "https://x.app?a=1", "://bad"} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) accepted", raw)
		}
	}
	if _, err := New(nil); err == nil {
		t.Error("New(nil) accepted")
	}
}
