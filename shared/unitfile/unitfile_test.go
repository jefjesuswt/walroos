package unitfile

import "testing"

func TestArgs(t *testing.T) {
	got := Args([]string{"sh", "-c", `echo "$HOME" 100% \done`})
	want := `"sh" "-c" "echo \"$$HOME\" 100%% \\done"`
	if got != want {
		t.Fatalf("Args = %s, want %s", got, want)
	}
}
