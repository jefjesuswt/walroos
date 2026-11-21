// Package envref expands references in environment values, like Railway's: ${{shop-db.URL}} is
// the URL variable of the service shop-db in the same project, ${{shared.STRIPE_KEY}} a variable
// shared by the whole project. They're resolved on each deploy, so a value stays current when
// what it points at changes.
package envref

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// A reference is ${{<service>.<KEY>}}, spaces allowed inside the braces.
var refRe = regexp.MustCompile(`\$\{\{\s*([a-z0-9][a-z0-9-]{0,62})\.([A-Za-z_][A-Za-z0-9_]{0,63})\s*\}\}`)

// Ref is one reference found in a value.
type Ref struct{ Service, Key string }

func (r Ref) String() string { return "${{" + r.Service + "." + r.Key + "}}" }

// Find lists the references in s, in order.
func Find(s string) []Ref {
	var out []Ref
	for _, m := range refRe.FindAllStringSubmatch(s, -1) {
		out = append(out, Ref{m[1], m[2]})
	}
	return out
}

// Vars are what references can name: service → KEY → value.
type Vars map[string]map[string]string

// Expand replaces every reference in s with its value, or fails on the first one vars doesn't have.
func Expand(s string, vars Vars) (string, error) {
	var missing error
	out := refRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := refRe.FindStringSubmatch(m)
		v, err := vars.lookup(Ref{sub[1], sub[2]})
		if err != nil && missing == nil {
			missing = err
		}
		return v
	})
	return out, missing
}

// Check fails if s names something vars doesn't have.
func Check(s string, vars Vars) error {
	for _, r := range Find(s) {
		if _, err := vars.lookup(r); err != nil {
			return err
		}
	}
	return nil
}

func (vars Vars) lookup(r Ref) (string, error) {
	svc, ok := vars[r.Service]
	if !ok {
		return "", fmt.Errorf("%s: no service %s in this project", r, r.Service)
	}
	v, ok := svc[r.Key]
	if !ok {
		return "", fmt.Errorf("%s: %s has no %s (it has %s)", r, r.Service, r.Key, strings.Join(slices.Sorted(maps.Keys(svc)), ", "))
	}
	return v, nil
}
