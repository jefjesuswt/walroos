package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadDevcontainer(t *testing.T) {
	repo := t.TempDir()
	if dc, err := readDevcontainer(repo, ""); dc != nil || err != nil {
		t.Fatalf("no devcontainer: got %+v, %v; want nil, nil (base image)", dc, err)
	}

	writeFile(t, filepath.Join(repo, ".devcontainer/devcontainer.json"), `{
		// JSONC: comments and trailing commas are allowed
		"build": { "dockerfile": "Dockerfile", },
		"remoteUser": "dev",
		"containerEnv": { "FOO": "bar" },
		"forwardPorts": [3000, "db:5432"],
		"postCreateCommand": "bun install",
		"mounts": [
			"type=volume,source=cache,target=/cache",
			{ "source": "npm", "target": "/home/dev/.npm" },
		],
	}`)
	dc, err := readDevcontainer(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if dc.Build.Dockerfile != "Dockerfile" || dc.RemoteUser != "dev" || dc.ContainerEnv["FOO"] != "bar" || len(dc.ForwardPorts) != 2 {
		t.Fatalf("parsed = %+v", dc)
	}
	if want := (command{"/bin/sh", "-c", "bun install"}); !slices.Equal(dc.PostCreateCommand, want) {
		t.Fatalf("postCreateCommand = %q, want %q", dc.PostCreateCommand, want)
	}
	if want := []mount{"type=volume,source=cache,target=/cache", "type=volume,source=npm,target=/home/dev/.npm"}; !slices.Equal(dc.Mounts, want) {
		t.Fatalf("mounts = %q, want %q", dc.Mounts, want)
	}
	if dc.dir != filepath.Join(repo, ".devcontainer") {
		t.Fatalf("dir = %q", dc.dir)
	}

	writeFile(t, filepath.Join(repo, "custom.json"), `{"image": "alpine", "postCreateCommand": ["make", "deps"]}`)
	dc, err = readDevcontainer(repo, "custom.json")
	if err != nil || dc.Image != "alpine" || !slices.Equal(dc.PostCreateCommand, command{"make", "deps"}) {
		t.Fatalf("explicit path: %+v, %v", dc, err)
	}

	if _, err := readDevcontainer(repo, "missing.json"); err == nil {
		t.Error("an explicit path that doesn't exist must fail, not fall back to the base image")
	}
	writeFile(t, filepath.Join(repo, "compose.json"), `{"dockerComposeFile": "compose.yml"}`)
	if _, err := readDevcontainer(repo, "compose.json"); err == nil {
		t.Error("a config with neither image nor dockerfile must fail")
	}
}

func TestDevcontainerRejectsHostMounts(t *testing.T) {
	repo := t.TempDir()
	for _, m := range []string{
		`"type=bind,source=/home,target=/h"`,
		`"source=/etc,target=/e, type=bind"`,
		`"source=x,target=/x"`, // no type
		`"type=volume,source=/,target=/h,type=bind"`,
		`{"type": "bind", "source": "/", "target": "/host"}`,
	} {
		writeFile(t, filepath.Join(repo, "dc.json"), `{"image": "alpine", "mounts": [`+m+`]}`)
		if _, err := readDevcontainer(repo, "dc.json"); err == nil {
			t.Errorf("mount %s was accepted", m)
		}
	}
	writeFile(t, filepath.Join(repo, "dc.json"), `{"image": "alpine", "mounts": ["type=tmpfs,target=/tmp/x", {"source": "cache", "target": "/c"}]}`)
	if _, err := readDevcontainer(repo, "dc.json"); err != nil {
		t.Errorf("volume and tmpfs mounts rejected: %v", err)
	}
}

func TestMetadataRemoteUser(t *testing.T) {
	for label, want := range map[string]string{
		`[{"id":"ghcr.io/devcontainers/features/node:2"},{"remoteUser":"node"}]`: "node",
		`[{"remoteUser":"a"},{"remoteUser":"b"}]`:                                "b", // later wins
		`[{"containerUser":"app"}]`:                                              "app",
		`[{"remoteUser":"dev"},{"containerUser":"app"}]`:                         "dev", // remoteUser set anywhere beats containerUser
		`[{"containerUser":"app","remoteUser":"dev"}]`:                           "dev", // same fragment: remoteUser wins
		``:              "", // no label: <no value> is not JSON either
		`<no value>`:    "",
		`{"not":"arr"}`: "",
	} {
		if got := metadataRemoteUser(label); got != want {
			t.Errorf("metadataRemoteUser(%s) = %q, want %q", label, got, want)
		}
	}
}

func TestInsideRepo(t *testing.T) {
	repo := "/vol/src"
	for p, ok := range map[string]bool{
		"/vol/src/.devcontainer/Dockerfile": true,
		"/vol/src":                          true,
		"/vol/src/../../etc/passwd":         false,
		"/etc/passwd":                       false,
		"/vol/src-other/Dockerfile":         false,
	} {
		if _, err := inside(repo, filepath.Clean(p)); (err == nil) != ok {
			t.Errorf("inside(%q) ok=%v, want %v", p, err == nil, ok)
		}
	}
}

func TestRepoName(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/me/app.git": "app",
		"https://github.com/me/app/":    "app",
		"git@github.com:me/my.app.git":  "my.app",
		"/tmp/x/repo":                   "repo",
		"https://example.com/":          "example.com",
		"weird url with spaces":         "src",
	} {
		if got := repoName(url); got != want {
			t.Errorf("repoName(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestBaseImageTagFollowsContainerfile(t *testing.T) {
	orig := baseContainerfile
	t.Cleanup(func() { baseContainerfile = orig })

	a := BaseImage()
	if a != BaseImage() || !strings.HasPrefix(a, "localhost/walroos-base:") {
		t.Fatalf("tag %q is not stable or not local", a)
	}
	baseContainerfile = append(append([]byte{}, orig...), "\n# edited\n"...)
	if BaseImage() == a {
		t.Fatal("editing the Containerfile must change the tag, or nodes keep a stale base image")
	}
}
