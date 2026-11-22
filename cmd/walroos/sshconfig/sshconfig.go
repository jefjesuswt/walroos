// Package sshconfig implements `walroos ssh-config`: it asks the hub for the user's workspaces
// and prints the Host blocks to paste into ~/.ssh/config.
package sshconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/jefjesuswt/walroos/cmd/walroos/hubconn"
	"github.com/spf13/cobra"
)

var (
	hub       string
	proxy     string
	sshPort   int
	hostSuff  string
	zed       bool
	zedWrite  bool
	zedConfig string
	sshConfig string
)

var Cmd = &cobra.Command{
	Use:   "ssh-config",
	Short: "Print SSH config blocks for your workspaces",
	Long: `Writes a Host block for every workspace into ~/.ssh/config, so ` + "`ssh <workspace>.walroos`" + ` works:

    walroos ssh-config --hub <hub> --write

The blocks go between "# BEGIN walroos" and "# END walroos" lines, replaced on every run (the
rest of the file is kept; symlinks are followed). Blocks for *.walroos hosts pasted earlier are
dropped, since the section has the current ones. Without --write the blocks are printed.

The hub identifies you by your tailnet login, so no token is needed. If the tailnet isn't
reachable directly but tailscaled's userspace proxy answers on localhost:1055
(tailscaled --tun=userspace-networking), it goes through it and each block gets a ProxyCommand
(needs openbsd-netcat). --proxy sets another proxy.

Zed's "Open Remote" opens the remote $HOME unless the connection lists a project. --zed prints
Zed's ssh_connections entries, each with its workspace's repo folder; --zed --write puts them in
Zed's settings.json (comments and other settings kept, symlinks followed, stale entries for
deleted workspaces removed):

    walroos ssh-config --zed --write`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if zed {
			return runZed(cmd)
		}
		if !zedWrite {
			out, err := Generate(cmd.Context(), hub, proxy, sshPort, hostSuff)
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		}
		return runWrite(cmd)
	},
}

func init() {
	Cmd.Flags().StringVar(&hub, "hub", "walroos-hub", "Hub address on the tailnet (http:// is assumed)")
	Cmd.Flags().StringVar(&proxy, "proxy", "", "HTTP/SOCKS proxy for the tailnet, e.g. localhost:1055 (adds ProxyCommand to each block)")
	Cmd.Flags().IntVar(&sshPort, "port", 2222, "The agent's SSH gateway port")
	Cmd.Flags().StringVar(&hostSuff, "suffix", ".walroos", "Suffix for the Host alias, so `ssh <workspace><suffix>` works")
	Cmd.Flags().BoolVar(&zed, "zed", false, "Print Zed's ssh_connections entries (each workspace opens on its repo) instead of SSH config")
	Cmd.Flags().BoolVar(&zedWrite, "write", false, "Update ~/.ssh/config in place (with --zed: Zed's settings.json)")
	Cmd.Flags().StringVar(&sshConfig, "ssh-config", "", "SSH config file for --write (default: ~/.ssh/config)")
	Cmd.Flags().StringVar(&zedConfig, "zed-settings", "", "Zed's settings.json (default: $XDG_CONFIG_HOME/zed/settings.json or ~/.config/zed/settings.json)")
}

func runZed(cmd *cobra.Command) error {
	list, _, err := fetch(cmd.Context(), hub, proxy)
	if err != nil {
		return err
	}
	if !zedWrite {
		b, _ := json.MarshalIndent(zedEntries(list, hostSuff), "", "  ")
		fmt.Println(string(b))
		fmt.Fprintln(os.Stderr, `# Zed's "ssh_connections" entries; --write puts them in its settings.json`)
		return nil
	}
	path := zedConfig
	if path == "" {
		path = zedSettingsPath()
	}
	changed, warnings, err := writeZed(path, list, hostSuff)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if err != nil {
		return err
	}
	if changed {
		fmt.Printf("Updated %s: %d workspace(s) in Zed's Open Remote, each with its repo folder.\n", path, len(list))
	} else {
		fmt.Printf("%s is already up to date.\n", path)
	}
	return nil
}

func runWrite(cmd *cobra.Command) error {
	list, used, err := fetch(cmd.Context(), hub, proxy)
	if err != nil {
		return err // the file isn't touched when the hub can't be asked
	}
	path := sshConfig
	if path == "" {
		path = sshConfigPath()
	}
	changed, dropped, err := writeSSH(path, managedBlock(hostBlocks(list, used, sshPort, hostSuff)), hostSuff)
	if err != nil {
		return err
	}
	for _, h := range dropped {
		fmt.Fprintf(os.Stderr, "# removed the Host %s block outside the walroos section\n", h)
	}
	if !changed {
		fmt.Printf("%s is already up to date.\n", path)
		return nil
	}
	names := make([]string, 0, len(list))
	for _, ws := range list {
		if cmpOr(ws.NodeAddress, ws.NodeName) != "" { // as hostBlocks
			names = append(names, ws.Name+hostSuff)
		}
	}
	if len(names) == 0 {
		fmt.Printf("Updated %s: no workspaces, so the walroos section is gone.\n", path)
	} else {
		fmt.Printf("Updated %s: ssh %s\n", path, strings.Join(names, ", "))
	}
	return nil
}

// workspace is the part of the hub's answer this command needs.
type workspace struct {
	Name        string `json:"name"`
	NodeName    string `json:"node_name"`
	NodeAddress string `json:"node_address"` // tailnet IP from the node's heartbeats
	State       string `json:"state"`
	Folder      string `json:"folder"` // repo path inside the container; "" when its node didn't answer
}

// Generate fetches the workspaces and formats the config. proxy is host:port or a URL.
func Generate(ctx context.Context, hub, proxy string, port int, suffix string) (string, error) {
	list, used, err := fetch(ctx, hub, proxy)
	if err != nil {
		return "", err
	}
	return Format(list, used, port, suffix), nil
}

// fetch asks the hub for the caller's workspaces and returns the proxy it went through ("" for
// none): directly, or through tailscaled's userspace proxy (hubconn.Reach).
func fetch(ctx context.Context, hub, proxy string) ([]workspace, string, error) {
	hub = hubconn.URL(hub)
	return hubconn.Reach(ctx, hub, proxy, func(ctx context.Context, c *http.Client) ([]workspace, error) {
		return get(ctx, c, hub)
	})
}

// get is one GET /api/workspaces with client.
func get(ctx context.Context, client *http.Client, hub string) ([]workspace, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hub+"/api/workspaces", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return nil, &hubconn.Answer{Msg: fmt.Sprintf("the hub doesn't know you: %s", e.Error)}
	}
	if resp.StatusCode == http.StatusBadGateway { // tailscaled's proxy found nothing at that name
		return nil, fmt.Errorf("nothing answered at %s on the tailnet (502 from the proxy)", hub)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &hubconn.Answer{Msg: fmt.Sprintf("hub answered %d", resp.StatusCode)}
	}
	var list []workspace
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

// Format renders the Host blocks, sorted by name so the output is stable.
// A ProxyCommand is only added when a proxy is configured: plain tailnet machines don't need it.
func Format(list []workspace, proxy string, port int, suffix string) string {
	return printedHeader + " — or let `walroos ssh-config --write` keep them in ~/.ssh/config\n" + hostBlocks(list, proxy, port, suffix)
}

// hostBlocks is one Host block per workspace, each preceded by a blank line.
func hostBlocks(list []workspace, proxy string, port int, suffix string) string {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	var b bytes.Buffer
	for _, ws := range list {
		// The tailnet IP, not the node's name: a node on tsnet is "walroos-<name>" on the tailnet,
		// and older hubs only send the name.
		node := cmpOr(ws.NodeAddress, ws.NodeName)
		if node == "" { // the hub always sends it; be safe rather than print an empty HostName
			continue
		}
		fmt.Fprintf(&b, "\nHost %s%s\n", ws.Name, suffix)
		fmt.Fprintf(&b, "  HostName %s\n", node)
		fmt.Fprintf(&b, "  Port %d\n", port)
		fmt.Fprintf(&b, "  User %s\n", ws.Name)
		if proxy != "" {
			fmt.Fprintf(&b, "  ProxyCommand nc -X 5 -x %s %%h %%p\n", hubconn.NormalizeProxy(proxy))
		}
	}
	return b.String()
}

func cmpOr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}
