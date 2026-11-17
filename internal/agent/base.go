package agent

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// baseContainerfile is the walroos base image (Debian, git, curl, mise, sftp-server, user dev),
// for repos without a devcontainer.json. See base/Containerfile.
//
//go:embed base/Containerfile
var baseContainerfile []byte

// BaseImage is the local tag of the base image. It carries a hash of the Containerfile, so a
// new walroos version with a changed Containerfile builds a fresh image instead of reusing a stale one.
func BaseImage() string {
	sum := sha256.Sum256(baseContainerfile)
	return "localhost/walroos-base:" + hex.EncodeToString(sum[:6])
}

// baseMu stops two workspaces created at once from building the base image twice.
var baseMu sync.Mutex

// ensureBaseImage builds the base image on this node the first time a workspace needs it.
// ponytail: built locally on each node (a minute or two, once); a published image would need a registry and CI.
func ensureBaseImage(ctx context.Context) (string, error) {
	baseMu.Lock()
	defer baseMu.Unlock()

	image := BaseImage()
	if _, err := podman(ctx, "image", "exists", image); err == nil {
		pruneOldBaseImages(ctx, image) // an old one may have been freed since (its workspace deleted)
		return image, nil
	}

	dir, err := os.MkdirTemp("", "walroos-base-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "Containerfile"), baseContainerfile, 0o644); err != nil {
		return "", err
	}
	if _, err := podman(ctx, "build", "-t", image, dir); err != nil {
		return "", fmt.Errorf("build base image: %w", err)
	}
	pruneOldBaseImages(ctx, image)
	return image, nil
}

// pruneOldBaseImages removes the other tags of keep's repository: base images from older
// Containerfiles. podman refuses (and we ignore it) while a workspace still uses one; it goes
// the next time a workspace asks for the base image.
func pruneOldBaseImages(ctx context.Context, keep string) {
	repo := keep[:strings.LastIndex(keep, ":")]
	out, _ := podman(ctx, "images", "--format", "{{.Repository}}:{{.Tag}}", "--filter", "reference="+repo)
	for _, img := range strings.Fields(out) {
		if img != keep {
			podman(ctx, "rmi", img)
		}
	}
}
