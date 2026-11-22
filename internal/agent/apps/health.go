package apps

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent/host"
)

var runningFor = 5 * time.Second // how long a HealthRunning release must stay up; tests shorten it

// relayTo runs the relay inside container's network namespace, connected to addr: the health
// checks' way in, since apps publish no ports. The relay writes one 0 byte once it's connected.
func (a *Apps) relayTo(ctx context.Context, container, addr string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	pid, err := host.Podman(ctx, "inspect", "--format", "{{.State.Pid}}", container)
	if err != nil || pid == "" || pid == "0" {
		return nil, nil, nil, fmt.Errorf("%s is not running", container)
	}
	args := append([]string{"nsenter", "--net=/proc/" + pid + "/ns/net", "--"}, a.relay...)
	args = append(args, "", addr) // no chroot: 127.0.0.1 needs no name lookup
	if os.Geteuid() != 0 {
		args = append([]string{"podman", "unshare"}, args...)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("connect: %w", err)
	}
	return cmd, stdin, stdout, nil
}

// connects reports whether something accepts connections on the container's port.
func (a *Apps) connects(ctx context.Context, container string, port int) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd, stdin, stdout, err := a.relayTo(ctx, container, "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	b := make([]byte, 1)
	_, rerr := io.ReadFull(stdout, b)
	stdin.Close()
	stdout.Close()
	cmd.Wait()
	if rerr != nil || b[0] != 0 {
		return fmt.Errorf("nothing accepts connections on port %d", port)
	}
	return nil
}

// waitTCP waits until the container accepts connections on port: the check for what isn't HTTP.
func (a *Apps) waitTCP(ctx context.Context, container string, port int, log io.Writer) error {
	fmt.Fprintf(log, "health check: TCP 127.0.0.1:%d\n", port)
	return poll(ctx, log, func() error { return a.connects(ctx, container, port) }, func(last error) error {
		return fmt.Errorf("health check: nothing accepted connections on port %d within %s: %v", port, healthTimeout, last)
	})
}

// waitRunning waits until the container has run for runningFor without restarting: the check for
// workers, which answer nothing. A crash loop restarts it (Restart=always), so its start time moves.
func (a *Apps) waitRunning(ctx context.Context, container string, log io.Writer) error {
	fmt.Fprintf(log, "health check: running for %s\n", runningFor)
	var since, started string
	return poll(ctx, log, func() error {
		out, err := host.Podman(ctx, "inspect", "--format", "{{.State.Running}} {{.State.StartedAt}}", container)
		running, at, _ := strings.Cut(out, " ")
		if err != nil || running != "true" {
			since = ""
			return fmt.Errorf("%s is not running", container)
		}
		if at != started {
			started, since = at, time.Now().Format(time.RFC3339Nano)
		}
		if t, _ := time.Parse(time.RFC3339Nano, since); time.Since(t) < runningFor {
			return fmt.Errorf("up for %s", time.Since(t).Round(time.Second))
		}
		return nil
	}, func(last error) error {
		return fmt.Errorf("health check: %s didn't keep running for %s within %s: %v", container, runningFor, healthTimeout, last)
	})
}

// poll runs check every second until it passes or healthTimeout ends, logging now and then.
func poll(ctx context.Context, log io.Writer, check func() error, timedOut func(last error) error) error {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	for i := 0; ; i++ {
		last := check()
		if last == nil {
			fmt.Fprintln(log, "health check: ok")
			return nil
		}
		if i%10 == 9 {
			fmt.Fprintf(log, "health check: still waiting (%v)\n", last)
		}
		select {
		case <-ctx.Done():
			return timedOut(last)
		case <-time.After(time.Second):
		}
	}
}
