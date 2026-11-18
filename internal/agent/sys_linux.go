//go:build linux

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// startPTY starts cmd on a new pseudo-terminal of the given size and returns its master side.
// podman sees a real terminal, so `podman exec -t` puts it in raw mode and forwards resizes.
func startPTY(cmd *exec.Cmd, cols, rows uint32) (*os.File, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	fd := int(ptmx.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil { // unlockpt
		ptmx.Close()
		return nil, fmt.Errorf("unlockpt: %w", err)
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN) // ptsname
	if err != nil {
		ptmx.Close()
		return nil, fmt.Errorf("ptsname: %w", err)
	}
	tty, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		ptmx.Close()
		return nil, err
	}
	defer tty.Close() // the child keeps its copy
	setWinsize(ptmx, cols, rows)

	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true} // Ctty 0: the child's stdin
	if err := cmd.Start(); err != nil {
		ptmx.Close()
		return nil, err
	}
	return ptmx, nil
}

// chroot makes dir the root, so name lookups read that filesystem's /etc/hosts and resolv.conf.
func chroot(dir string) error {
	if err := syscall.Chroot(dir); err != nil {
		return err
	}
	return os.Chdir("/")
}

func setWinsize(ptmx *os.File, cols, rows uint32) error {
	return unix.IoctlSetWinsize(int(ptmx.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
}
