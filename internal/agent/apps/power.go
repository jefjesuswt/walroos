package apps

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
)

var ErrPower = errors.New("unknown action: stop, start or restart")

const (
	wanted   = "\nWantedBy=default.target\n"
	unwanted = "\n# Stopped with walroos: it stays stopped, even after a reboot, until it's started.\n# WantedBy=default.target\n"
)

// setWanted turns the unit's start on boot on or off: a stopped app stays stopped after a
// reboot. Only the [Install] line changes.
func setWanted(unit []byte, on bool) []byte {
	if on {
		return bytes.Replace(unit, []byte(unwanted), []byte(wanted), 1)
	}
	return bytes.Replace(unit, []byte(wanted), []byte(unwanted), 1)
}

// Power stops, starts or restarts the app's running release. A deploy or a rollback starts the
// new release whatever this left.
func (a *Apps) Power(ctx context.Context, app, action string) error {
	if action != agent.AppStop && action != agent.AppStart && action != agent.AppRestart {
		return ErrPower
	}
	unlock, err := a.q.Lock(app) // not in the middle of a deploy's swap
	if err != nil {
		return err
	}
	defer unlock()
	units, err := a.units(app)
	if err != nil {
		return err
	}
	if len(units) == 0 {
		return ErrNotFound
	}
	for _, c := range units {
		if err := a.wantOnBoot(c, action != agent.AppStop); err != nil {
			return err
		}
	}
	if err := host.Run(ctx, nil, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	for _, c := range units {
		if err := host.Run(ctx, nil, "systemctl", "--user", action, c+".service"); err != nil {
			return err
		}
	}
	return nil
}

func (a *Apps) wantOnBoot(container string, on bool) error {
	file := filepath.Join(a.q.Dir, container+".container")
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if next := setWanted(b, on); !bytes.Equal(next, b) {
		return os.WriteFile(file, next, 0o644)
	}
	return nil
}
