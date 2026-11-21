package envref

import (
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
	vars := Vars{
		"shop-db": {"URL": "postgres://u:p@shop-db:5432/shop", "HOST": "shop-db", "PORT": "5432"},
		"shared":  {"STRIPE_KEY": "sk_test"},
	}
	for _, c := range []struct{ in, want, err string }{
		{"${{shop-db.URL}}", "postgres://u:p@shop-db:5432/shop", ""},
		{"http://${{ shop-db.HOST }}:${{shop-db.PORT}}/x", "http://shop-db:5432/x", ""},
		{"key=${{shared.STRIPE_KEY}}", "key=sk_test", ""},
		{"no refs, $HOME and ${VAR} stay", "no refs, $HOME and ${VAR} stay", ""},
		{"${{api.URL}}", "", "no service api"},
		{"${{shop-db.PASSWORD}}", "", "has no PASSWORD (it has HOST, PORT, URL)"},
	} {
		got, err := Expand(c.in, vars)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("Expand(%q) error = %v, want %q", c.in, err, c.err)
			}
			if Check(c.in, vars) == nil {
				t.Errorf("Check(%q) passed", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("Expand(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if refs := Find("${{a.X}} and ${{b-2.Y_1}}"); len(refs) != 2 || refs[1] != (Ref{"b-2", "Y_1"}) || refs[0].String() != "${{a.X}}" {
		t.Errorf("Find = %+v", refs)
	}
}
