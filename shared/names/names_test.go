package names

import (
	"strings"
	"testing"
)

func TestNames(t *testing.T) {
	for s, want := range map[string]bool{"a": true, "shop-db": true, "1x": true, "-a": false, "a-": false, "A": false, "a_b": false, strings.Repeat("a", 63): true, strings.Repeat("a", 64): false} {
		if DNSLabel(s) != want {
			t.Errorf("DNSLabel(%q) = %v", s, !want)
		}
	}
	for s, want := range map[string]bool{"DATABASE_URL": true, "_X": true, "1X": false, "lower": false} {
		if EnvKey(s) != want {
			t.Errorf("EnvKey(%q) = %v", s, !want)
		}
	}
	for s, want := range map[string]bool{"orders_rw": true, "1db": false, "Orders": false} {
		if SQLIdent(s) != want {
			t.Errorf("SQLIdent(%q) = %v", s, !want)
		}
	}
}
