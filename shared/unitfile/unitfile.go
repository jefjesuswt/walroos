// Package unitfile writes values into systemd unit files (and Quadlets, which systemd reads).
package unitfile

import "strings"

// Quote makes an argument safe in ExecStart or a Quadlet's Exec=: systemd splits on spaces and
// reads % (specifiers) and $ (variables).
func Quote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s)
	return `"` + s + `"`
}

// Args is args quoted and joined, for one ExecStart or Exec= line.
func Args(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = Quote(a)
	}
	return strings.Join(q, " ")
}
