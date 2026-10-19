package featureflag

import (
	"strings"
	"testing"
)

func TestValidKey(t *testing.T) {
	for _, key := range []string{"analytics_ai_enabled", "module:pos", "pos.offline-v2", "a", strings.Repeat("a", 80)} {
		if !ValidKey(key) {
			t.Errorf("ValidKey(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"", "Module:pos", "-flag", "flag key", "flag/../x", strings.Repeat("a", 81)} {
		if ValidKey(key) {
			t.Errorf("ValidKey(%q) = true, want false", key)
		}
	}
}
