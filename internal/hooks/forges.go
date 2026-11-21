package hooks

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jefjesuswt/walroos/internal/app"
)

// A forge is how one provider signs and describes a push.
type forge struct {
	name   string
	verify func(h http.Header, body []byte, secret string) bool
	parse  func(h http.Header, body []byte) (push, error)
}

var forges = []forge{
	{"github", hmacHeader("X-Hub-Signature-256", "sha256="), parseEvent("X-GitHub-Event", "push")},
	{"gitea", hmacHeader("X-Gitea-Signature", ""), parseEvent("X-Gitea-Event", "push")}, // Forgejo sends it too
	{"gitlab", tokenHeader("X-Gitlab-Token"), parseEvent("X-Gitlab-Event", "Push Hook")},
}

// hmacHeader: the header holds prefix + hex(HMAC-SHA256(secret, body)).
func hmacHeader(name, prefix string) func(http.Header, []byte, string) bool {
	return func(h http.Header, body []byte, secret string) bool {
		got, ok := strings.CutPrefix(h.Get(name), prefix)
		if !ok {
			return false
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return hmac.Equal([]byte(got), []byte(hex.EncodeToString(mac.Sum(nil))))
	}
}

// tokenHeader: the header holds the secret itself (GitLab's "secret token").
func tokenHeader(name string) func(http.Header, []byte, string) bool {
	return func(h http.Header, _ []byte, secret string) bool {
		return subtle.ConstantTimeCompare([]byte(h.Get(name)), []byte(secret)) == 1
	}
}

// push is the part of a push event that decides a deploy. All three forges use these names.
type push struct {
	event, ignore string // ignore: another event (GitHub's ping, a tag, an issue), which deploys nothing
	Ref           string `json:"ref"`
	After         string `json:"after"`
	Repository    struct {
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Project struct { // GitLab
		DefaultBranch string `json:"default_branch"`
	} `json:"project"`
	sha string
}

func parseEvent(header, pushEvent string) func(http.Header, []byte) (push, error) {
	return func(h http.Header, body []byte) (push, error) {
		p := push{event: h.Get(header)}
		if p.event != pushEvent {
			p.ignore = cmp.Or(p.event, "(none)")
			return p, nil
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return p, fmt.Errorf("not a push payload: %w", err)
		}
		p.sha = p.After
		return p, nil
	}
}

// skip says why this event deploys nothing, or "" if it deploys.
func (p push) skip(t app.HookTarget) string {
	if p.ignore != "" {
		return "event " + p.ignore + " deploys nothing; only pushes do"
	}
	branch, ok := strings.CutPrefix(p.Ref, "refs/heads/")
	if !ok {
		return p.Ref + " isn't a branch"
	}
	want := t.Branch
	if want == "" {
		want = p.Repository.DefaultBranch + p.Project.DefaultBranch
	}
	if branch != want {
		return "push to " + branch + "; the app deploys " + want
	}
	if strings.Trim(p.sha, "0") == "" {
		return "the branch was deleted"
	}
	return ""
}
