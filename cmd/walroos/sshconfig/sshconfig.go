// Package sshconfig implements `walroos ssh-config`: it asks the hub for the user's workspaces
// and prints the Host blocks to paste into ~/.ssh/config.
package sshconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

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
)

var Cmd = &cobra.Command{
	Use:   "ssh-config",
	Short: "Print SSH config blocks for your workspaces",
	Long: `Prints Host blocks for every workspace, to append to ~/.ssh/config:

    walroos ssh-config >> ~/.ssh/config

The hub identifies you by your tailnet login, so no token is needed. If this machine reaches
the tailnet through a userspace proxy (tailscaled --tun=userspace-networking), pass --proxy
and each block gets a ProxyCommand (needs openbsd-netcat):

    walroos ssh-config --proxy localhost:1055

Zed's "Open Remote" opens the remote $HOME unless the connection lists a project. --zed prints
Zed's ssh_connections entries, each with its workspace's repo folder; --zed --write puts them in
Zed's settings.json (comments and other settings kept, symlinks followed, stale entries for
deleted workspaces removed):

    walroos ssh-config --zed --write`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if zedWrite && !zed {
			return fmt.Errorf("--write goes with --zed")
		}
		if zed {
			return runZed(cmd)
		}
		out, err := Generate(cmd.Context(), hub, proxy, sshPort, hostSuff)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&hub, "hub", "walroos-hub", "Hub address on the tailnet (http:// is assumed)")
	Cmd.Flags().StringVar(&proxy, "proxy", "", "HTTP/SOCKS proxy for the tailnet, e.g. localhost:1055 (adds ProxyCommand to each block)")
	Cmd.Flags().IntVar(&sshPort, "port", 2222, "The agent's SSH gateway port")
	Cmd.Flags().StringVar(&hostSuff, "suffix", ".walroos", "Suffix for the Host alias, so `ssh <workspace><suffix>` works")
	Cmd.Flags().BoolVar(&zed, "zed", false, "Print Zed's ssh_connections entries (each workspace opens on its repo) instead of SSH config")
	Cmd.Flags().BoolVar(&zedWrite, "write", false, "With --zed: update Zed's settings.json in place")
	Cmd.Flags().StringVar(&zedConfig, "zed-settings", "", "Zed's settings.json (default: $XDG_CONFIG_HOME/zed/settings.json or ~/.config/zed/settings.json)")
}

func runZed(cmd *cobra.Command) error {
	list, err := fetch(cmd.Context(), hub, proxy)
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
	list, err := fetch(ctx, hub, proxy)
	if err != nil {
		return "", err
	}
	return Format(list, proxy, port, suffix), nil
}

// fetch asks the hub for the caller's workspaces.
func fetch(ctx context.Context, hub, proxy string) ([]workspace, error) {
	if !strings.Contains(hub, "://") {
		hub = "http://" + hub
	}
	hub = strings.TrimSuffix(hub, "/")

	client, err := newClient(proxy, 15*time.Second)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hub+"/api/workspaces", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if proxy == "" {
			return nil, fmt.Errorf("reach %s: %w (is this machine on the tailnet? for userspace tailscaled, pass --proxy localhost:1055)", hub, err)
		}
		return nil, fmt.Errorf("reach %s through %s: %w", hub, proxy, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return nil, fmt.Errorf("the hub doesn't know you: %s", e.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub answered %d", resp.StatusCode)
	}
	var list []workspace
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func newClient(proxy string, timeout time.Duration) (*http.Client, error) {
	c := &http.Client{Timeout: timeout}
	if proxy == "" {
		return c, nil
	}
	u, err := url.Parse("http://" + normalizeProxy(proxy))
	if err != nil {
		return nil, fmt.Errorf("--proxy %q: %w", proxy, err)
	}
	c.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	return c, nil
}

// normalizeProxy accepts localhost:1055, http://localhost:1055 or socks5://localhost:1055 and
// returns host:port: tailscale's userspace proxy serves HTTP and SOCKS5 on the same port.
func normalizeProxy(p string) string {
	p = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(p, "http://"), "socks5://"), "/")
	if _, _, err := net.SplitHostPort(p); err != nil {
		p = net.JoinHostPort(p, "1055")
	}
	return p
}

// Format renders the Host blocks, sorted by name so the output is stable.
// A ProxyCommand is only added when a proxy is configured: plain tailnet machines don't need it.
func Format(list []workspace, proxy string, port int, suffix string) string {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	var b bytes.Buffer
	b.WriteString("# walroos workspaces — append to ~/.ssh/config\n")
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
			fmt.Fprintf(&b, "  ProxyCommand nc -X 5 -x %s %%h %%p\n", normalizeProxy(proxy))
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
