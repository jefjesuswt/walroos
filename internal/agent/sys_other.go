//go:build !linux

package agent

import (
	"errors"
	"os"
	"os/exec"
)

// The agent runs on Linux; this only keeps the package building elsewhere.
func startPTY(*exec.Cmd, uint32, uint32) (*os.File, error) {
	return nil, errors.New("terminals are only supported on Linux nodes")
}

func setWinsize(*os.File, uint32, uint32) error { return nil }

func chroot(string) error { return errors.New("only supported on Linux nodes") }
