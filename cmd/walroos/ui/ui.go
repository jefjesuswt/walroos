// Package ui is `walroos ui`: the hub's panel on 127.0.0.1, for a machine whose browser can't
// reach the tailnet (tailscaled in userspace mode, next to another VPN). It forwards every
// request to the hub, directly or through tailscaled's proxy, so the hub still knows you by
// your tailnet identity.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/jefjesuswt/walroos/cmd/walroos/hubconn"
	"github.com/jefjesuswt/walroos/shared/whttp"
	"github.com/spf13/cobra"
)

var (
	hub, proxy string
	port       int
	noOpen     bool
)

var Cmd = &cobra.Command{
	Use:   "ui",
	Short: "Open the hub's panel from this machine, on 127.0.0.1",
	Long: `Serves the hub's panel on http://127.0.0.1:<port> and opens it in your browser.

Use it where the browser can't reach the tailnet: with tailscaled in userspace mode (as next to
another VPN), walroos goes through tailscaled's proxy on localhost:1055, like ssh-config. The hub
still sees your tailnet identity. Stop it with Ctrl+C.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target := hubconn.URL(hub)
		login, used, err := hubconn.Reach(cmd.Context(), target, proxy, whoAmI(target))
		if err != nil {
			return err
		}
		client, err := hubconn.Client(used, 0) // no timeout: logs and deploys stream
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return fmt.Errorf("listen on 127.0.0.1:%d: %w (another walroos ui? --port picks another)", port, err)
		}
		local := "http://" + ln.Addr().String()
		fmt.Printf("walroos panel for %s at %s (signed in as %s). Ctrl+C stops it.\n", target, local, login)
		if !noOpen {
			openBrowser(local)
		}
		return serve(cmd.Context(), ln, Handler(target, local, client.Transport))
	},
}

func init() {
	Cmd.Flags().StringVar(&hub, "hub", "walroos-hub", "Hub address on the tailnet (http:// is assumed)")
	Cmd.Flags().StringVar(&proxy, "proxy", "", "HTTP/SOCKS proxy for the tailnet (default: found by itself, localhost:1055)")
	Cmd.Flags().IntVar(&port, "port", 7480, "Port on 127.0.0.1")
	Cmd.Flags().BoolVar(&noOpen, "no-open", false, "Don't open the browser")
}

// whoAmI checks the hub answers as the hub and knows you: GET /api/me.
func whoAmI(target string) func(context.Context, *http.Client) (string, error) {
	return func(ctx context.Context, c *http.Client) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"/api/me", nil)
		if err != nil {
			return "", err
		}
		resp, err := c.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var me struct{ Login, Error string }
		jsonErr := json.NewDecoder(resp.Body).Decode(&me)
		switch {
		case resp.StatusCode == http.StatusBadGateway: // tailscaled's proxy found nothing there
			return "", fmt.Errorf("nothing answered at %s on the tailnet (502 from the proxy)", target)
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return "", &hubconn.Answer{Msg: "the hub doesn't know you: " + me.Error}
		case resp.StatusCode != http.StatusOK || jsonErr != nil || me.Login == "":
			return "", &hubconn.Answer{Msg: fmt.Sprintf("%s answered %d: is it a walroos hub?", target, resp.StatusCode)}
		}
		return me.Login, nil
	}
}

// Handler forwards everything to target as if the browser were there. Only requests for
// 127.0.0.1 or localhost are served (a DNS-rebinding page can't use it), and the browser's own
// Origin becomes the hub's, so the hub's cross-origin check still refuses other sites' writes
// (they keep their own Origin and Sec-Fetch-Site).
func Handler(target, local string, transport http.RoundTripper) http.Handler {
	to, _ := url.Parse(target)
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(to)
			r.Out.Host = to.Host
			if r.In.Header.Get("Origin") == local {
				r.Out.Header.Set("Origin", target)
			}
		},
		Transport:     transport,
		FlushInterval: -1, // deploy logs stream line by line
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			whttp.ErrorJSON(w, http.StatusBadGateway, "walroos ui can't reach the hub: "+err.Error())
		},
	}
	return whttp.AllowHosts(rp, "localhost")
}

func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openBrowser is best effort: the URL is printed anyway.
func openBrowser(u string) {
	name, args := "xdg-open", []string{u}
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", u}
	}
	if err := exec.Command(name, args...).Start(); err != nil {
		fmt.Println("Open it in your browser:", u)
	}
}
