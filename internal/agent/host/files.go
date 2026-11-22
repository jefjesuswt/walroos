package host

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxRepoFile = 256 << 10

// RepoFiles clones url shallowly and returns the files at paths (relative to the repo) that
// exist, up to 256 KiB each: what the hub reads from a repo without a node building it (a
// compose file and its env_files). A path outside the repo, a folder or a link out is skipped.
func RepoFiles(ctx context.Context, url, branch, token string, paths []string) (map[string]string, error) {
	dir, err := os.MkdirTemp("", "walroos-files-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	if _, err := GitClone(ctx, args, url, dir, token); err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, p := range paths {
		clean := path.Clean(p)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			continue
		}
		real, err := Inside(dir, filepath.Join(dir, filepath.FromSlash(clean)))
		if err != nil {
			continue
		}
		st, err := os.Stat(real)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if st.Size() > maxRepoFile {
			return nil, fmt.Errorf("%s is over %d KiB", clean, maxRepoFile>>10)
		}
		b, err := os.ReadFile(real)
		if err != nil {
			return nil, err
		}
		files[clean] = string(b)
	}
	return files, nil
}
