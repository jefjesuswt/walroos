package agent

import (
	"net/http"

	"github.com/jefjesuswt/walroos/shared/whttp"
)

var ErrNoAddress = whttp.Conflict("node has no tailnet address yet (no heartbeat received)")

// Client is how the hub calls node agents. HTTP must dial over the tailnet (tsnet's HTTPClient).
type Client struct {
	HTTP *http.Client
}
