package whttp

import (
	"net"
	"net/http"
	"strings"
)

// AllowHosts rejects requests whose Host isn't one of names or an IP literal. Auth by network
// identity (Tailscale WhoIs) trusts whoever connects, so without this a page on evil.com that
// re-resolves to the hub's IP (DNS rebinding) would call the API as the visitor.
func AllowHosts(next http.Handler, names ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		ok := net.ParseIP(strings.Trim(host, "[]")) != nil
		for _, n := range names {
			ok = ok || host == strings.ToLower(n)
		}
		if !ok {
			ErrorJSON(w, http.StatusMisdirectedRequest, "unknown host "+r.Host)
			return
		}
		next.ServeHTTP(w, r)
	})
}
