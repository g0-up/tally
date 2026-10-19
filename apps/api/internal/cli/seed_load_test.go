package cli

import (
	"strings"
	"testing"
)

// The flags are checked before the configuration is read or the database opened, so these runs
// need neither.
func TestSeedLoadRejectsBadFlags(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--skus", "20001"}, "--skus must be between 20 and 20000"},
		{[]string{"--skus", "5"}, "--skus must be between 20 and 20000"},
		{[]string{"--tenants", "0"}, "--tenants"},
		{[]string{"--moves", "0"}, "--moves"},
		{[]string{"--slug-prefix", ""}, "--slug-prefix"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, err := run(t, map[string]string{}, append([]string{"seed", "load"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("seed load %v = %q, %v; want an error naming %q", tc.args, out, err, tc.want)
			}
		})
	}
}

func TestSeedLoadRejectsArguments(t *testing.T) {
	if _, err := run(t, map[string]string{}, "seed", "load", "extra"); err == nil {
		t.Fatal("seed load accepted a positional argument")
	}
}

func TestVerifyToken(t *testing.T) {
	token, err := verifyToken("https://load-42-1.tally.test/verify-email?token=abc123")
	if err != nil || token != "abc123" {
		t.Fatalf("verifyToken = %q, %v", token, err)
	}
	for _, link := range []string{"https://load-42-1.tally.test/verify-email", "https://x.test/v?token=", "%zz?token=secret"} {
		_, err := verifyToken(link)
		if err == nil {
			t.Errorf("verifyToken(%q) found a token", link)
			continue
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "http") {
			t.Errorf("verifyToken error quotes the link: %v", err)
		}
	}
}

// Commands that create tenants with generated data run only with APP_ENV set explicitly to
// development or test: production and an unset APP_ENV are refused before they read the rest of
// the configuration or open the database.
func TestSeedCommandsRefuseProduction(t *testing.T) {
	for _, args := range [][]string{{"seed", "load"}, {"seed", "dev"}} {
		for name, env := range map[string]map[string]string{
			"production": {"APP_ENV": "production"},
			"unset":      {},
			"blank":      {"APP_ENV": " "},
		} {
			t.Run(strings.Join(args, " ")+" "+name, func(t *testing.T) {
				out, err := run(t, env, args...)
				if err == nil || !strings.Contains(err.Error(), "runs only with APP_ENV set to development or test") {
					t.Fatalf("%v with %v = %q, %v; want a refusal", args, env, out, err)
				}
			})
		}
	}
	// An explicit development or test passes the guard and fails later, on the missing database.
	for _, appEnv := range []string{"development", "test"} {
		out, err := run(t, map[string]string{"APP_ENV": appEnv}, "seed", "dev")
		if err == nil || strings.Contains(err.Error(), "runs only with APP_ENV") {
			t.Errorf("seed dev with APP_ENV=%s = %q, %v; want past the guard", appEnv, out, err)
		}
	}
}
