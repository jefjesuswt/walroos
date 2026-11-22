// Package userservice runs a walroos command as a systemd --user service that starts on boot:
// `walroos node install` and `walroos hub install` both use it.
package userservice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/jefjesuswt/walroos/shared/unitfile"
)

// Unit is one service: walroos with Args, restarted when it fails.
type Unit struct {
	Name        string // walroos-node.service
	Description string
	Args        []string // after the walroos binary: node run, hub --data …
	// KillProcess keeps the processes the service started (a workspace's conmon) alive when
	// it restarts: systemd only signals the main process.
	KillProcess bool
}

// Text is the unit file for the walroos binary at exe.
func (u Unit) Text(exe string) string {
	args := []string{unitfile.Quote(exe)}
	for _, a := range u.Args {
		args = append(args, unitfile.Quote(a))
	}
	kill := ""
	if u.KillProcess {
		kill = "KillMode=process\n"
	}
	return `[Unit]
Description=` + u.Description + `
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=` + strings.Join(args, " ") + `
Restart=on-failure
RestartSec=10
` + kill + `
[Install]
WantedBy=default.target
`
}

// Install writes the unit for this binary, enables and starts it. It returns the unit's path.
func Install(ctx context.Context, u Unit) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cfg, "systemd", "user", u.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(u.Text(exe)), 0o644); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", u.Name}, {"restart", u.Name}} {
		if out, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput(); err != nil {
			return path, fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return path, nil
}

// Linger enables lingering, so user services start on boot and survive logout. loginctl can
// exit 0 even when polkit says no (no active session), so it reads the setting back. When it
// couldn't, it returns the command the user should run.
func Linger(ctx context.Context) (fix string) {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	exec.CommandContext(ctx, "loginctl", "enable-linger", u.Username).Run()
	out, _ := exec.CommandContext(ctx, "loginctl", "show-user", u.Username, "-p", "Linger", "--value").Output()
	if strings.TrimSpace(string(out)) == "yes" {
		return ""
	}
	return "sudo loginctl enable-linger " + u.Username
}

// Active reports whether the service is running now.
func Active(ctx context.Context, name string) bool {
	return exec.CommandContext(ctx, "systemctl", "--user", "is-active", "--quiet", name).Run() == nil
}
