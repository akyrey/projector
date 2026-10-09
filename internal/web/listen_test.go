package web_test

import (
	"testing"

	"github.com/akyrey/projector/internal/web"
)

func TestListenLocal(t *testing.T) {
	for _, tt := range []struct {
		addr string
		ok   bool
	}{
		{"127.0.0.1:0", true},
		{"localhost:0", true},
		{"[::1]:0", true},
		{"0.0.0.0:0", false},
		{":0", false},
		{"192.168.1.10:0", false},
		{"nonsense", false},
	} {
		ln, err := web.ListenLocal(tt.addr)
		if ln != nil {
			_ = ln.Close()
		}
		if (err == nil) != tt.ok {
			t.Errorf("ListenLocal(%q) err = %v, want ok=%v", tt.addr, err, tt.ok)
		}
	}
}

func TestNewToken(t *testing.T) {
	a, errA := web.NewToken()
	b, errB := web.NewToken()
	if errA != nil || errB != nil {
		t.Fatalf("NewToken: %v, %v", errA, errB)
	}
	if len(a) < 32 || a == b {
		t.Errorf("tokens %q and %q: want long and distinct", a, b)
	}
}
