package cli

import (
	"strings"
	"testing"
)

func TestReadPasswordTakesTheFirstLine(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"correct horse battery\n", "correct horse battery"},
		{"windows line\r\nsecond", "windows line"},
		{"no newline at all", "no newline at all"},
		{"  spaces are kept  \n", "  spaces are kept  "},
	}
	for _, tc := range tests {
		got, err := readPassword(strings.NewReader(tc.in))
		if err != nil || got != tc.want {
			t.Errorf("readPassword(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{"", "\n", "\r\nsecret"} {
		if _, err := readPassword(strings.NewReader(in)); err == nil {
			t.Errorf("readPassword(%q) succeeded, want an error", in)
		}
	}
}
