package hub

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jefjesuswt/walroos/cmd/walroos/userservice"
	"github.com/jefjesuswt/walroos/shared/ui"
	"github.com/spf13/cobra"
)

const serviceName = "walroos-hub.service"

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Run the hub as a systemd --user service that starts on boot",
	Long: `Writes ~/.config/systemd/user/` + serviceName + ` with this binary, --data and --hostname, enables
and starts it, and enables lingering so it runs without a login session.

Start the hub once in the foreground first (walroos hub --owner you@github) to log in to the
tailnet; install then keeps it running. Remove with: systemctl --user disable --now walroos-hub`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := os.Stat(filepath.Join(dataDir, "tsnet")); err != nil {
			return ui.WrapError("the hub hasn't joined the tailnet yet: run `walroos hub --owner you@github` once, open the login URL it prints, then install")
		}
		unit := userservice.Unit{Name: serviceName, Description: "walroos hub", Args: []string{"hub", "--data", dataDir, "--hostname", hostname}}
		path, err := userservice.Install(cmd.Context(), unit)
		if err != nil {
			return ui.WrapError("%w", err)
		}
		ui.PrintSuccess("Installed and started %s.", serviceName)
		ui.PrintKeyValue("Unit", path)
		fmt.Println("\n  Logs:  journalctl --user -u walroos-hub -f")
		if fix := userservice.Linger(cmd.Context()); fix != "" {
			fmt.Printf("\n  To start on boot without logging in, run:  %s\n", fix)
		}
		return nil
	},
}
