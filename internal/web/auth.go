package web

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// contentSecurityPolicy allows only the embedded page's own scripts, styles
// and API calls.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; " +
	"img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// loopbackHosts are the only Host header names accepted, which defeats DNS
// rebinding: a foreign page resolving its own name to 127.0.0.1 still sends
// its own name.
var loopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// guard sets security headers, enforces the loopback Host, and requires the
// token as a bearer header on /api/. The page and its assets are public: they
// hold no data, and the page reads the token from the URL fragment.
//
// The token is never accepted from a cookie: cookies are shared by every port
// on localhost, so any local dev server the browser visits would receive it.
// Since browsers never attach the header on their own, cross-site requests
// cannot carry credentials and need no CSRF or Origin checks.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.validToken(bearerToken(r)) {
			writeError(w, http.StatusUnauthorized, "unauthorized: open the link printed by `projector serve`")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validToken(got string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func bearerToken(r *http.Request) string {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return tok
}

func isLoopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	return loopbackHosts[strings.Trim(host, "[]")]
}
