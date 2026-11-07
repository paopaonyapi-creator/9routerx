// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package updater

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestDetectChannel_Matrix pins the distribution-channel heuristic
// (docs/updater-contract.md): npm marker wins, then missing/-dev version
// metadata, then a `go run` build-cache executable, then the packaged-binary
// default.
func TestDetectChannel_Matrix(t *testing.T) {
	okExec := func() (string, error) { return "/opt/9routerx/bin/9routerx", nil }
	goRunExec := func() (string, error) { return "/tmp/go-build123456/b001/exe/9routerx", nil }
	failedExec := func() (string, error) { return "", errors.New("no executable") }

	tests := []struct {
		name       string
		version    string
		exec       func() (string, error)
		npmMarked  bool
		want       DistChannel
		whyContain string
	}{
		{"packaged release binary", "1.9.16", okExec, false, ChannelBinary, "packaged"},
		{"npm marker wins over everything", "1.9.16", okExec, true, ChannelNpm, "9ROUTERX_NPM_INSTALL"},
		{"npm marker wins over a dev version", "", goRunExec, true, ChannelNpm, "9ROUTERX_NPM_INSTALL"},
		{"empty version means no version metadata", "", okExec, false, ChannelDev, "version metadata"},
		{"-dev suffix means source build", "1.9.17-dev", okExec, false, ChannelDev, "-dev"},
		{"go run executable is a source build", "1.9.16", goRunExec, false, ChannelDev, "go run"},
		{"unresolvable executable falls back to binary", "1.9.16", failedExec, false, ChannelBinary, "packaged"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := detectChannel(tt.version, tt.exec, tt.npmMarked)
			if got != tt.want {
				t.Errorf("detectChannel(%q, npm=%v) = %q, want %q (why=%q)", tt.version, tt.npmMarked, got, tt.want, why)
			}
			if !strings.Contains(why, tt.whyContain) {
				t.Errorf("reason %q does not mention %q", why, tt.whyContain)
			}
		})
	}
}

// TestCompareVersions_NumericSegments pins the spec's regression: segments
// compare numerically, never lexically — 0.5.112 is NEWER than 0.5.104 even
// though "112" < "104" as strings.
func TestCompareVersions_NumericSegments(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   int
	}{
		{"0.5.112", "0.5.104", 1},
		{"0.5.104", "0.5.112", -1},
		{"0.10.0", "0.9.0", 1},
		{"0.5.9", "0.5.10", -1},
		{"1.0.0", "1.0.0", 0},
		{"v0.5.112", "0.5.104", 1},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.v1, tt.v2); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

// TestParseCheckInterval_Matrix pins the UPDATE_CHECK_INTERVAL grammar: bare
// numbers are hours, duration strings pass through, "0" disables, and a
// unparsable value falls back to the 6h default with checking ON — a typo must
// never turn update checking off silently.
func TestParseCheckInterval_Matrix(t *testing.T) {
	tests := []struct {
		env     string
		want    time.Duration
		enabled bool
	}{
		{"", DefaultCheckInterval, true},
		{"6", 6 * time.Hour, true},
		{"12", 12 * time.Hour, true},
		{"0", 0, false},
		{"-3", 0, false},
		{"30m", 30 * time.Minute, true},
		{"1.5h", 90 * time.Minute, true},
		{"banana", DefaultCheckInterval, true},
	}
	for _, tt := range tests {
		got, enabled := ParseCheckInterval(tt.env)
		if got != tt.want || enabled != tt.enabled {
			t.Errorf("ParseCheckInterval(%q) = (%v, %v), want (%v, %v)", tt.env, got, enabled, tt.want, tt.enabled)
		}
	}
}

// TestErrorCause_Classification pins the failure taxonomy the structured log
// and /api/update/debug report. A check failure with no named cause is the
// silent death this exists to prevent.
func TestErrorCause_Classification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"dns", &net.DNSError{Err: "no such host", Name: "evil.example", IsNotFound: true}, "dns"},
		{"network", &url.Error{Op: "Get", URL: "https://x/y", Err: errors.New("connection refused")}, "network"},
		{"wrapped deadline", &url.Error{Op: "Get", URL: "https://x/y", Err: context.DeadlineExceeded}, "timeout"},
		{"http 429", errors.New("manifest HTTP 429"), "rate_limited"},
		{"http 404", errors.New("manifest HTTP 404"), "not_found"},
		{"other status", errors.New("manifest HTTP 503"), "http_status"},
		{"parse", errors.New("invalid character 'x' looking for beginning of value"), "parse"},
		{"unknown", errors.New("something odd"), "unknown"},
	}
	for _, tt := range tests {
		if got := errorCause(tt.err); got != tt.want {
			t.Errorf("%s: errorCause = %q, want %q", tt.name, got, tt.want)
		}
	}
}
