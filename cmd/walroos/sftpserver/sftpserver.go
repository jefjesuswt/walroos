// Package sftpserver is `walroos sftp-server`: an SFTP server on stdin/stdout. The node agent
// copies its own binary into workspace containers whose image has no sftp-server (the
// devcontainers images don't), so scp, sftp and editors that upload over SFTP (Zed) work
// without changing the image. It runs as the workspace user, inside the container.
package sftpserver

import (
	"errors"
	"io"
	"os"

	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:    "sftp-server",
	Short:  "SFTP on stdin/stdout (the SSH gateway runs it inside workspaces)",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var opts []sftp.ServerOption
		if home, err := os.UserHomeDir(); err == nil {
			opts = append(opts, sftp.WithServerWorkingDirectory(home)) // like OpenSSH: relative paths from $HOME
		}
		s, err := sftp.NewServer(stdio{os.Stdin, os.Stdout}, opts...)
		if err != nil {
			return err
		}
		if err := s.Serve(); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	},
}

type stdio struct {
	io.Reader
	io.WriteCloser
}
