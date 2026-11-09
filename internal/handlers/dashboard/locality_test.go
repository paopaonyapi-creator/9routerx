// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalityNeverTrustsHost(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	t.Setenv("TRUST_PROXY", "false")
	t.Setenv("TRUST_CLOUDFLARE", "false")
	t.Setenv("9ROUTERX_PEER_TOKEN", "")
	t.Setenv("NINEROUTER_PEER_TOKEN", "")
	for _, tc := range []struct {
		peer  string
		local bool
	}{
		{"203.0.113.10:1234", false},
		{"127.0.0.1:1234", true},
		{"[::1]:1234", true},
		{"[::ffff:127.0.0.1]:1234", true},
		{"malformed", false},
	} {
		t.Run(tc.peer, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/auth/login", nil)
			req.RemoteAddr = tc.peer
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			if got := nodeRequestIsLocal(req); got != tc.local {
				t.Errorf("provider locality = %v, want %v", got, tc.local)
			}
			if got := loginRequestIsLocal(req); got != tc.local {
				t.Errorf("login locality = %v, want %v", got, tc.local)
			}
		})
	}
}

func TestLoginLocalityUsesTrustedClientIP(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1/32")
	t.Setenv("TRUST_PROXY", "false")
	t.Setenv("TRUST_CLOUDFLARE", "false")
	t.Setenv("9ROUTERX_PEER_TOKEN", "")
	t.Setenv("NINEROUTER_PEER_TOKEN", "")
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/auth/login", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	if loginRequestIsLocal(req) {
		t.Fatal("trusted proxy's remote client must not be treated as local")
	}
	if !nodeRequestIsLocal(req) {
		t.Fatal("provider-node locality must remain based on the direct peer")
	}
}
