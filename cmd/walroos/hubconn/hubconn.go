// Package hubconn finds how this machine reaches the hub: directly on the tailnet, or through
// tailscaled's proxy when it runs in userspace mode (no route to the tailnet, as next to another
// VPN). ssh-config and ui use it.
package hubconn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultProxy is where tailscaled in userspace mode serves its HTTP/SOCKS5 proxy (the port its
// docs use). Tests point it elsewhere.
var DefaultProxy = "localhost:1055"

// Answer is an answer from the hub (it was reached), as opposed to not reaching it.
type Answer struct{ Msg string }

func (e *Answer) Error() string { return e.Msg }

func IsAnswer(err error) bool {
	var a *Answer
	return errors.As(err, &a)
}

// URL is hub with http:// when it has no scheme, without a trailing slash.
func URL(hub string) string {
	if !strings.Contains(hub, "://") {
		hub = "http://" + hub
	}
	return strings.TrimSuffix(hub, "/")
}

// Reach runs try against the hub and returns its result and the proxy it went through ("" for
// none). With proxy it only goes through it. Without, it tries the tailnet directly, briefly; a
// machine on userspace tailscaled has no route to it, so if that fails and tailscaled's proxy
// answers on DefaultProxy, it goes through it. try returns an *Answer when the hub answered but
// said no: then there's nothing else to try.
func Reach[T any](ctx context.Context, hub, proxy string, try func(context.Context, *http.Client) (T, error)) (T, string, error) {
	var zero T
	if proxy != "" {
		c, err := Client(proxy, 15*time.Second)
		if err != nil {
			return zero, "", err
		}
		v, err := try(ctx, c)
		if err != nil && !IsAnswer(err) {
			return zero, "", fmt.Errorf("reach %s through %s: %w", hub, proxy, err)
		}
		return v, proxy, err
	}
	direct, _ := Client("", 4*time.Second)
	v, err := try(ctx, direct)
	if err == nil || IsAnswer(err) {
		return v, "", err
	}
	if c, derr := net.DialTimeout("tcp", DefaultProxy, time.Second); derr == nil {
		c.Close()
		viaProxy, _ := Client(DefaultProxy, 15*time.Second)
		if v, perr := try(ctx, viaProxy); perr == nil || IsAnswer(perr) {
			fmt.Fprintf(os.Stderr, "# %s isn't reachable directly; went through tailscaled's proxy at %s\n", hub, DefaultProxy)
			return v, DefaultProxy, perr
		}
	}
	return zero, "", fmt.Errorf("reach %s: %w (is this machine on the tailnet? with userspace tailscaled, its proxy must be up: --proxy localhost:1055)", hub, err)
}

// Client is an HTTP client to the hub, through proxy if set. timeout 0 means none (a proxy
// that streams).
func Client(proxy string, timeout time.Duration) (*http.Client, error) {
	c := &http.Client{Timeout: timeout}
	if proxy == "" {
		return c, nil
	}
	u, err := url.Parse("http://" + NormalizeProxy(proxy))
	if err != nil {
		return nil, fmt.Errorf("--proxy %q: %w", proxy, err)
	}
	c.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	return c, nil
}

// NormalizeProxy accepts localhost:1055, http://localhost:1055 or socks5://localhost:1055 and
// returns host:port: tailscale's userspace proxy serves HTTP and SOCKS5 on the same port.
func NormalizeProxy(p string) string {
	p = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(p, "http://"), "socks5://"), "/")
	if _, _, err := net.SplitHostPort(p); err != nil {
		p = net.JoinHostPort(p, "1055")
	}
	return p
}
