package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
)

// ListenLocal listens on addr, refusing any host that is not a loopback
// address: the dashboard runs commands and must not be reachable from the
// network.
func ListenLocal(addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("refusing to listen on %q: only loopback addresses (127.0.0.1, ::1, localhost) are allowed", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return ln, nil
}

// tokenBytes is the entropy of a dashboard token.
const tokenBytes = 32

// NewToken returns a random token for one server start.
func NewToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
