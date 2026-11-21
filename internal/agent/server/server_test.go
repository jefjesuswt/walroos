package server_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/server"
)

func TestAgentAuth(t *testing.T) {
	const hub, nodeToken = "nHub", "wlr_node_secret"
	caller := hub
	api := server.New(func(context.Context, string) (string, error) {
		if caller == "" {
			return "", errors.New("not on the tailnet")
		}
		return caller, nil
	}, nodeToken)

	get := func(bearer string) int {
		req := httptest.NewRequest("GET", "/v1/info", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	good := agent.TokenHash(nodeToken)

	if code := get(good); code != 503 {
		t.Fatalf("before the hub is known: %d, want 503", code)
	}
	api.SetHubID(hub)

	cases := []struct {
		name, caller, bearer string
		want                 int
	}{
		{"hub with token hash", hub, good, 200},
		{"other tailnet node", "nLaptop", good, 403},
		{"not on the tailnet", "", good, 403},
		{"raw token instead of its hash", hub, nodeToken, 401},
		{"no token", hub, "", 401},
	}
	for _, c := range cases {
		caller = c.caller
		if code := get(c.bearer); code != c.want {
			t.Errorf("%s: %d, want %d", c.name, code, c.want)
		}
	}
}
