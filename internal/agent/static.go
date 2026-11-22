package agent

import (
	"path"
	"strings"
)

// StaticPort is what a static site's server listens on, in every release: rollbacks keep working
// when the settings change.
const StaticPort = 8080

// StaticBuild makes an app from a repo with no Dockerfile, like Netlify: install and build with
// Node or Bun (the app's env vars are there during the build), then serve the output folder with
// walroos's own static server. Every field is optional: they're found from the repo.
type StaticBuild struct {
	Runtime string `json:"runtime,omitempty"` // node or bun; "" picks bun with a bun lockfile or BUN_VERSION, else node (versions: agent/apps/static.go)
	Install string `json:"install,omitempty"` // "" = the package manager's install (npm ci, pnpm, yarn, bun)
	Build   string `json:"build,omitempty"`   // "" = <package manager> run build, if package.json has a build script
	Publish string `json:"publish,omitempty"` // folder to serve, relative to the repo; "" = the first of dist/*/browser, dist, build, out, public with an index.html
	SPA     bool   `json:"spa,omitempty"`     // paths with no file get index.html, for client-side routing
}

func (b *StaticBuild) valid() bool {
	if b == nil {
		return true
	}
	command := func(s string) bool { return len(s) <= 1000 && !strings.ContainsRune(s, 0) }
	publish := b.Publish == "" || pathRe.MatchString(b.Publish) && path.Clean(b.Publish) == b.Publish &&
		!path.IsAbs(b.Publish) && b.Publish != ".." && !strings.HasPrefix(b.Publish, "../")
	return (b.Runtime == "" || b.Runtime == "node" || b.Runtime == "bun") && command(b.Install) && command(b.Build) && publish
}
