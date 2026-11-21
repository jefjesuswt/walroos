package apps

import (
	"fmt"
	"os"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent/sshgw"
)

// TestMain lets health checks use this test binary as the relay (`walroos node relay`), like
// a.relay = []string{os.Args[0]} in the podman tests asks.
func TestMain(m *testing.M) {
	if os.Getenv("WALROOS_TEST_RELAY") != "" {
		if err := sshgw.Relay(os.Args[1], os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
