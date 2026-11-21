package agent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// A build node keeps the images it builds in a registry (distribution) bound to localhost.
// The tailnet reaches it only through RegistryHandler: read-only, one app per token.
const RegistryPort = "5000" // on the tailnet

// PullToken lets a prod node pull app's images from a build node. It's keyed with the build
// node's token hash, which only that node and the hub know, so the hub hands it out and the
// build node checks it without storing anything. ponytail: never expires; scope is one app.
func PullToken(buildTokenHash, app string) string {
	m := hmac.New(sha256.New, []byte(buildTokenHash))
	io.WriteString(m, "registry pull walroos-app-"+app)
	return hex.EncodeToString(m.Sum(nil))
}
