package agent

import (
	"errors"
	"net/http"
)

var ErrNoAddress = errors.New("node has no tailnet address yet (no heartbeat received)")

// Client is how the hub calls node agents. HTTP must dial over the tailnet (tsnet's HTTPClient).
type Client struct {
	HTTP *http.Client
}
