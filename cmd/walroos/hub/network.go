package hub

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/shared/ui"
	"tailscale.com/net/netns"
	"tailscale.com/tsnet"
)

// network is where the hub listens and how it knows who's calling: the tailnet, or localhost in --dev.
type network struct {
	ln       net.Listener
	identify auth.Identify
	url      string
	id       string        // our tailnet StableNodeID; agents only accept calls from it
	hosts    []string      // Host names we answer to, besides IPs (DNS rebinding)
	agents   *agent.Client // calls nodes' agents over the same network
	close    func()
	// funnel publishes a listener on the internet (Tailscale Funnel) and says at what URL;
	// nil in --dev.
	funnel func() (net.Listener, string, error)
}

func listen(ctx context.Context) (network, error) {
	if dev {
		return listenDev()
	}
	return listenTailnet(ctx)
}

func listenDev() (network, error) {
	slog.Warn("DEV MODE: no Tailscale, no auth. Never expose this port.", "addr", devAddr, "user", devLogin)
	ln, err := net.Listen("tcp", devAddr)
	if err != nil {
		return network{}, ui.WrapError("listen: %w", err)
	}
	return network{
		ln: ln, identify: auth.Static(devLogin), url: "http://" + devAddr,
		id:     "dev", // ponytail: no tailnet in --dev, so agents can't verify or be reached
		hosts:  []string{"localhost"},
		agents: &agent.Client{HTTP: &http.Client{Timeout: 30 * time.Second}},
		close:  func() {},
	}, nil
}

func listenTailnet(ctx context.Context) (network, error) {
	// tsnet is userspace-only (no TUN, no kernel routes), so its sockets don't need to
	// bypass the host's routing. Bypassing (SO_MARK / SO_BINDTODEVICE to the default
	// interface) skips host VPNs like NordVPN, whose firewall then drops the traffic.
	// Same as `tailscaled --tun=userspace-networking`.
	netns.SetEnabled(false)
	ts := &tsnet.Server{
		Hostname: hostname,
		Dir:      filepath.Join(dataDir, "tsnet"),
		AuthKey:  authKey(),
		Logf:     func(string, ...any) {}, // backend noise; UserLogf still prints the login URL
	}
	nw, err := upTailnet(ctx, ts)
	if err != nil {
		ts.Close()
		return network{}, err
	}
	nw.close = func() { ts.Close() }
	nw.funnel = func() (net.Listener, string, error) {
		ln, err := ts.ListenFunnel("tcp", ":443", tsnet.FunnelOnly()) // only the internet: the tailnet uses :80
		if err != nil {
			return nil, "", err
		}
		return ln, "https://" + nw.hosts[1], nil
	}
	return nw, nil
}

func upTailnet(ctx context.Context, ts *tsnet.Server) (network, error) {
	slog.Info("Connecting to the tailnet...", "hostname", hostname)
	status, err := ts.Up(ctx)
	if err != nil {
		return network{}, ui.WrapError("tailscale up: %w", err)
	}
	lc, err := ts.LocalClient()
	if err != nil {
		return network{}, ui.WrapError("tailscale local client: %w", err)
	}
	// ponytail: plain HTTP on :80 is already encrypted by WireGuard; ListenTLS(:443) when browsers need https (clipboard API, etc.)
	ln, err := ts.Listen("tcp", ":80")
	if err != nil {
		return network{}, ui.WrapError("tailscale listen: %w", err)
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	// The full MagicDNS name and IP work when the short name doesn't resolve (phones
	// with "Use Tailscale DNS" off, other search domains).
	slog.Info("On the tailnet", "dns", dns, "ips", status.TailscaleIPs)
	client := ts.HTTPClient()
	client.Timeout = 30 * time.Second
	return network{
		ln: ln, identify: auth.Tailscale(lc), url: "http://" + hostname, id: string(status.Self.ID),
		hosts: []string{hostname, dns}, agents: &agent.Client{HTTP: client},
	}, nil
}

func authKey() string {
	if k := os.Getenv("TS_AUTHKEY"); k != "" {
		return k
	}
	return os.Getenv("TS_AUTH_KEY") // name used by earlier walroos versions
}
