package sshgw

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
)

// SFTP for scp, sftp and editors that upload over it (Zed). An image's own sftp-server wins; most
// devcontainers images have none, so the gateway copies the walroos binary into the container
// once and runs `walroos sftp-server` there, as the workspace user. That works in existing
// workspaces, without changing or rebuilding the image.
//
// ponytail: the helper runs only where the walroos binary can: a CGO build needs glibc, so
// Alpine/musl images need a static build (CGO_ENABLED=0) or openssh-sftp-server in the image.

// sftpPaths are where distros put OpenSSH's sftp-server.
const sftpPaths = "/usr/lib/openssh/sftp-server /usr/libexec/openssh/sftp-server /usr/lib/ssh/sftp-server /usr/libexec/sftp-server /usr/lib/sftp-server"

// sftpHelperGlob matches the helper copies; the name carries a hash of the agent binary, so a new
// walroos version gets a new copy instead of running a stale one.
const sftpHelperGlob = "/usr/local/bin/.walroos-sftp-*"

// sftpServer is what the subsystem runs: the image's sftp-server, else the helper, else a reason.
const sftpServer = `for p in ` + sftpPaths + `; do [ -x "$p" ] && exec "$p"; done; ` +
	`for h in ` + sftpHelperGlob + `; do [ -x "$h" ] && { "$h" sftp-server; r=$?; ` +
	`[ $r -ne 126 ] && [ $r -ne 127 ] && exit $r; ` +
	`echo "walroos's SFTP helper can't run in this image (a musl image needs a static walroos: CGO_ENABLED=0); install openssh-sftp-server" >&2; exit 127; }; done; ` +
	`echo "no sftp-server in this workspace image (install openssh-sftp-server)" >&2; exit 127`

var (
	sftpHashOnce sync.Once
	sftpHash     string // first 12 hex of sha256 of our own binary; "" if it can't be read
	sftpMu       sync.Mutex
)

func selfHash() string {
	sftpHashOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		f, err := os.Open(exe)
		if err != nil {
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err == nil {
			sftpHash = hex.EncodeToString(h.Sum(nil))[:12]
		}
	})
	return sftpHash
}

// ensureSFTPHelper copies the agent binary into the workspace container when its image has no
// sftp-server and the current helper isn't there yet. Errors are for the log: the subsystem
// script still explains what's missing to the client.
func ensureSFTPHelper(ctx context.Context, ws agent.Workspace) error {
	hash := selfHash()
	if hash == "" {
		return fmt.Errorf("can't hash the agent binary")
	}
	ctr, helper := "ws-"+ws.Name, "/usr/local/bin/.walroos-sftp-"+hash
	sftpMu.Lock() // two SFTP sessions opening at once copy once
	defer sftpMu.Unlock()

	check := `for p in ` + sftpPaths + ` "$1"; do [ -x "$p" ] && exit 0; done; exit 1`
	if _, err := host.Podman(ctx, "exec", ctr, "/bin/sh", "-c", check, "sh", helper); err == nil {
		return nil // the image has one, or this version's helper is already there
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// As root: /usr/local/bin is root's. Old versions' copies go, so upgrades don't pile up.
	if _, err := host.Podman(ctx, "exec", "--user", "0", ctr, "/bin/sh", "-c",
		`rm -f `+sftpHelperGlob+`; mkdir -p /usr/local/bin`); err != nil {
		return err
	}
	if _, err := host.Podman(ctx, "cp", exe, ctr+":"+helper); err != nil {
		return err
	}
	_, err = host.Podman(ctx, "exec", "--user", "0", ctr, "chmod", "0755", helper)
	return err
}

// isSFTP reports whether a valid session request is the sftp subsystem: sessionArgv accepts no
// other subsystem.
func isSFTP(reqType string) bool { return reqType == "subsystem" }
