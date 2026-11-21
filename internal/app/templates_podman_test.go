//go:build linux

package app_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jefjesuswt/walroos/internal/app"
)

// TestTemplatesPodman starts every template's image the way a deploy would (its variables, its
// volumes) and waits for its health path to answer 2xx/3xx: the catalog's ports and paths are
// what the images really do. Pulls every image.
//
//	WALROOS_PODMAN_TEST=1 go test ./internal/app -run TemplatesPodman -v
func TestTemplatesPodman(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1")
	}
	for _, tpl := range app.Templates {
		t.Run(tpl.ID, func(t *testing.T) {
			t.Parallel()
			name := fmt.Sprintf("walroos-tpl-%s-%d", tpl.ID, time.Now().UnixNano()%1e6)
			args := []string{"run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1::%d", tpl.Port)}
			for _, e := range tpl.Env {
				v := e.Value
				if e.Generate {
					v = "Gen3ratedPassw0rd42"
				}
				args = append(args, "-e", e.Key+"="+v)
			}
			for _, v := range tpl.Volumes {
				args = append(args, "-v", name+"-"+v.Name+":"+v.Target)
			}
			t.Cleanup(func() {
				exec.Command("podman", "rm", "-f", "-t", "1", name).Run()
				for _, v := range tpl.Volumes {
					exec.Command("podman", "volume", "rm", "-f", name+"-"+v.Name).Run()
				}
			})
			if out, err := exec.Command("podman", append(args, tpl.Image)...).CombinedOutput(); err != nil {
				t.Fatalf("podman run: %v\n%s", err, out)
			}
			out, err := exec.Command("podman", "port", name, fmt.Sprint(tpl.Port)).Output()
			if err != nil {
				t.Fatalf("podman port: %v", err)
			}
			addr := strings.TrimSpace(strings.Split(string(out), "\n")[0])
			client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			var last string
			for ctx.Err() == nil {
				resp, err := client.Get("http://" + addr + tpl.HealthPath)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 400 {
						return
					}
					last = resp.Status
				} else {
					last = err.Error()
				}
				time.Sleep(2 * time.Second)
			}
			logs, _ := exec.Command("podman", "logs", "--tail", "20", name).CombinedOutput()
			t.Fatalf("%s never answered GET %s with 2xx/3xx (last: %s)\n%s", tpl.Image, tpl.HealthPath, last, logs)
		})
	}
}
