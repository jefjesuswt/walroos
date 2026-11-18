package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jefjesuswt/walroos/cmd/walroos/hub"
	"github.com/jefjesuswt/walroos/cmd/walroos/node"
	"github.com/jefjesuswt/walroos/cmd/walroos/sshconfig"
	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "walroos",
	Short:         "Self-hosted dev workspaces and deploys on Podman",
	Long:          "Walroos runs dev workspaces and deploys apps on your own machines, over a Tailscale network.\n\n  walroos hub        control plane (API + UI)\n  walroos node join  add this machine to a hub\n  walroos node run   node agent\n  walroos ssh-config SSH config for your workspaces",
	Version:       version.Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(hub.Cmd, node.Cmd, sshconfig.Cmd)
}
