// Package sshconfig implements `walroos ssh-config`: it asks the hub for the user's workspaces
// and prints the Host blocks to paste into ~/.ssh/config.
package sshconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// defaultProxy is where tailscaled in userspace mode serves its HTTP/SOCKS5 proxy (the port its
// docs and walroos use). Tests point it elsewhere.
var defaultProxy = "localhost:1055"

// fetch asks the hub for the caller's workspaces and returns the proxy it went through ("" for
// none). Without --proxy it tries the tailnet directly, briefly; a machine on userspace
// tailscaled has no route to it, so if that fails and tailscaled's proxy answers on
// localhost:1055, it goes through it, and the SSH blocks get the matching ProxyCommand.
func fetch(ctx context.Context, hub, proxy string) ([]workspace, string, error) {
	if !strings.Contains(hub, "://") {
		hub = "http://" + hub
	}
	hub = strings.TrimSuffix(hub, "/")

	if proxy != "" {
		list, err := get(ctx, hub, proxy, 15*time.Second)
		if err != nil && !isHubAnswer(err) {
			return nil, "", fmt.Errorf("reach %s through %s: %w", hub, proxy, err)
		}
		return list, proxy, err
	}
	list, err := get(ctx, hub, "", 4*time.Second)
	if err == nil || isHubAnswer(err) {
		return list, "", err
	}
	if c, derr := net.DialTimeout("tcp", defaultProxy, time.Second); derr == nil {
		c.Close()
		if list, perr := get(ctx, hub, defaultProxy, 15*time.Second); perr == nil || isHubAnswer(perr) {
			fmt.Fprintf(os.Stderr, "# %s isn't reachable directly; went through tailscaled's proxy at %s\n", hub, defaultProxy)
			return list, defaultProxy, perr
		}
	}
	return nil, "", fmt.Errorf("reach %s: %w (is this machine on the tailnet? with userspace tailscaled, its proxy must be up: --proxy localhost:1055)", hub, err)
}

// hubError is an answer from the hub (it was reached), as opposed to not reaching it.
type hubError struct{ msg string }

func (e *hubError) Error() string { return e.msg }

func isHubAnswer(err error) bool {
	var he *hubError
	return errors.As(err, &he)
}

// get is one GET /api/workspaces, directly or through proxy.
func get(ctx context.Context, hub, proxy string, timeout time.Duration) ([]workspace, error) {
	client, err := newClient(proxy, timeout)
	if err != nil {
		return nil, err
	}
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
		return nil, &hubError{fmt.Sprintf("the hub doesn't know you: %s", e.Error)}
	}
	if resp.StatusCode == http.StatusBadGateway { // tailscaled's proxy found nothing at that name
		return nil, fmt.Errorf("nothing answered at %s on the tailnet (502 from the proxy)", hub)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &hubError{fmt.Sprintf("hub answered %d", resp.StatusCode)}
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
	return "# walroos workspaces — or let `walroos ssh-config --write` keep them in ~/.ssh/config\n" + hostBlocks(list, proxy, port, suffix)
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
