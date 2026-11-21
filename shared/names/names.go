// Package names validates the names walroos hands to DNS, podman and env files.
package names

import "regexp"

var (
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	envKey   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	sqlIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

// DNSLabel: lowercase letters, digits and '-', 1 to 63, not starting or ending with '-'. Workspaces,
// apps, databases, projects and nodes are named like this: the name is a hostname somewhere.
func DNSLabel(s string) bool { return dnsLabel.MatchString(s) }

// EnvKey: an environment variable name, A-Z, 0-9 and '_', not starting with a digit.
func EnvKey(s string) bool { return envKey.MatchString(s) }

// SQLIdent: a database or user name that needs no quoting.
func SQLIdent(s string) bool { return sqlIdent.MatchString(s) }
