// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package daemon

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPortConflictFingerprints(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       GatewayKind
		hint       bool
	}{
		{"X", "<title>9RouterX</title>", GatewayX, true},
		{"legacy Next", "<title>9Router</title><script src=\"/_next/main.js\"></script>", GatewayLegacy, true},
		{"unrelated Next", "<title>Other</title><script src=\"/_next/main.js\"></script>", GatewayUnknown, false},
		{"unrelated", "hello", GatewayUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			// HTTP evidence is tested separately from process fallback: this test executable
			// itself legitimately contains 9routerx in its path.
			got := fingerprintGateway(server.URL)
			if got != tc.kind {
				t.Fatalf("kind=%s, want %s", got, tc.kind)
			}
			p := PortConflict{Kind: got, Port: 20128, Busy: true}
			if strings.Contains(p.Error(), "service take-over") != tc.hint {
				t.Fatal(p.Error())
			}
			if !strings.Contains(p.Error(), "PORT=20129") {
				t.Fatal(p.Error())
			}
		})
	}
}
func TestPortConflictDoesNotFollowExternalRedirect(t *testing.T) {
	called := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; fmt.Fprint(w, "9routerx") }))
	defer remote.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, remote.URL, http.StatusFound) }))
	defer server.Close()
	if got := fingerprintGateway(server.URL); got != GatewayUnknown {
		t.Fatal(got)
	}
	if called {
		t.Fatal("fingerprint followed another origin")
	}
}
func TestRefuseUnverifiedProcessKill(t *testing.T) {
	p := PortConflict{Port: 1, Process: ProcessInfo{PID: 1}}
	if err := RequestPortStop(p); err == nil {
		t.Fatal("unverified graceful signal accepted")
	}
	if err := ForcePortStop(p); err == nil {
		t.Fatal("unverified force kill accepted")
	}
}
func TestProcessGatewayDoesNotClassifyArbitraryArguments(t *testing.T) {
	for _, tc := range []struct {
		p    ProcessInfo
		want GatewayKind
	}{
		{ProcessInfo{Executable: `C:\\bin\\9routerx.exe`}, GatewayX},
		{ProcessInfo{Executable: `C:\\bin\\node.exe`, CommandLine: `node C:\\apps\\9router\\server.js`}, GatewayLegacy},
		{ProcessInfo{Executable: `C:\\bin\\python.exe`, CommandLine: `python unrelated.py --notes=9routerx`}, GatewayUnknown},
		{ProcessInfo{Executable: `C:\\bin\\9routerx.test.exe`}, GatewayUnknown},
	} {
		if got := processGateway(tc.p); got != tc.want {
			t.Fatalf("%+v: got %s, want %s", tc.p, got, tc.want)
		}
	}
}

func TestAlternativePortBoundary(t *testing.T) {
	if AlternativePort(65535) != 20129 {
		t.Fatal("invalid fallback port")
	}
}
