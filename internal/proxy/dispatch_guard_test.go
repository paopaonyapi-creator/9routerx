// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package proxy

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/providers"
)

// TestAssertDispatchURLAllowed pins the URL-level dispatch guard: scheme and
// userinfo checks, the private-host refusal, the operator opt-in, and the
// metadata carve-out that survives the opt-in. Every case that would need DNS
// uses a literal address or a name the guard answers statically, so the table
// runs the same offline as on a networked host.
func TestAssertDispatchURLAllowed(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		allowLocal bool
		wantErr    string // "" means allowed; otherwise a substring of the error
	}{
		{
			name: "public https allowed",
			url:  "https://93.184.216.34/v1/chat/completions",
		},
		{
			name: "public http allowed",
			url:  "http://93.184.216.34/v1/chat/completions",
		},
		{
			name:    "loopback blocked by default",
			url:     "http://127.0.0.1:11434/v1/chat/completions",
			wantErr: "internal host",
		},
		{
			name:       "loopback allowed when local permitted",
			url:        "http://127.0.0.1:11434/v1/chat/completions",
			allowLocal: true,
		},
		{
			name:    "rfc1918 blocked by default",
			url:     "http://192.168.1.10:8000/v1",
			wantErr: "internal host",
		},
		{
			name:       "rfc1918 allowed when local permitted",
			url:        "http://192.168.1.10:8000/v1",
			allowLocal: true,
		},
		{
			name:       "aws metadata blocked even when local permitted",
			url:        "http://169.254.169.254/latest/meta-data/iam/security-credentials/",
			allowLocal: true,
			wantErr:    "cloud instance metadata endpoint",
		},
		{
			name:    "aws metadata blocked by default",
			url:     "http://169.254.169.254/latest/meta-data/",
			wantErr: "blocked by SSRF guard",
		},
		{
			name:       "gcp metadata name blocked even when local permitted",
			url:        "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token",
			allowLocal: true,
			wantErr:    "cloud instance metadata endpoint",
		},
		{
			name:       "alibaba metadata blocked even when local permitted",
			url:        "http://100.100.100.200/latest/meta-data/",
			allowLocal: true,
			wantErr:    "cloud instance metadata endpoint",
		},
		{
			name:       "azure platform vip blocked even when local permitted",
			url:        "http://168.63.129.16/machine/",
			allowLocal: true,
			wantErr:    "cloud instance metadata endpoint",
		},
		{
			name:       "aws metadata ipv6 blocked even when local permitted",
			url:        "http://[fd00:ec2::254]/latest/api/token",
			allowLocal: true,
			wantErr:    "cloud instance metadata endpoint",
		},
		{
			name:    "embedded userinfo rejected",
			url:     "http://user:pass@93.184.216.34/v1",
			wantErr: "embedded credentials",
		},
		{
			name:    "invalid url rejected",
			url:     "http://",
			wantErr: "no host",
		},
		{
			name:    "empty url rejected",
			url:     "   ",
			wantErr: "empty upstream URL",
		},
		{
			name:    "non-http scheme rejected",
			url:     "file:///etc/passwd",
			wantErr: "unsupported scheme",
		},
		{
			name:    "ftp scheme rejected",
			url:     "ftp://93.184.216.34/pub",
			wantErr: "unsupported scheme",
		},
		{
			name:       "metadata decimal literal blocked even when local permitted",
			url:        "http://2852060926/latest/meta-data/",
			allowLocal: true,
			wantErr:    "cloud instance metadata endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertDispatchURLAllowed(tt.url, tt.allowLocal)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("AssertDispatchURLAllowed(%q, %v) = %v, want allowed", tt.url, tt.allowLocal, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("AssertDispatchURLAllowed(%q, %v) = nil, want error containing %q", tt.url, tt.allowLocal, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("AssertDispatchURLAllowed(%q, %v) = %v, want error containing %q", tt.url, tt.allowLocal, err, tt.wantErr)
			}
			if !errors.Is(err, ErrDispatchBlocked) {
				t.Fatalf("error %v does not wrap ErrDispatchBlocked", err)
			}
		})
	}
}

// TestDispatchAllowLocalSources pins how the exemption is resolved: the
// setting, the env fallback, a local-by-design config, or the context marker.
func TestDispatchAllowLocalSources(t *testing.T) {
	t.Run("default refuses", func(t *testing.T) {
		if dispatchAllowLocal(context.Background(), nil) {
			t.Fatal("default dispatchAllowLocal = true, want false")
		}
	})

	t.Run("setting allows", func(t *testing.T) {
		SetAllowPrivateProviderURLs(true)
		defer SetAllowPrivateProviderURLs(false)
		if !dispatchAllowLocal(context.Background(), nil) {
			t.Fatal("setting did not grant allowLocal")
		}
	})

	t.Run("env fallback allows", func(t *testing.T) {
		t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "true")
		if !AllowPrivateProviderURLs() {
			t.Fatal("env fallback not honored")
		}
	})

	t.Run("local-by-design config allows", func(t *testing.T) {
		cfg := &providers.ProviderConfig{LocalByDesign: true}
		if !dispatchAllowLocal(context.Background(), cfg) {
			t.Fatal("LocalByDesign config did not grant allowLocal")
		}
	})

	t.Run("context marker allows", func(t *testing.T) {
		if !dispatchAllowLocal(WithAllowLoopback(context.Background()), nil) {
			t.Fatal("WithAllowLoopback context did not grant allowLocal")
		}
	})
}

// TestIsLocalDispatchProvider pins the self-hosted set the guard exempts.
func TestIsLocalDispatchProvider(t *testing.T) {
	for _, p := range []string{"ollama-local", "sdwebui", "searxng", "comfyui", "tortoise", "lmstudio", "vllm"} {
		if !IsLocalDispatchProvider(p) {
			t.Errorf("IsLocalDispatchProvider(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"openai", "deepseek", "ollama", "groq", "custom-node"} {
		if IsLocalDispatchProvider(p) {
			t.Errorf("IsLocalDispatchProvider(%q) = true, want false", p)
		}
	}
}

// TestApplyDispatchGuardBlocksByDefault runs the ForwardOpenAI hook against a
// private URL with the setting off: it must refuse with a 403-shaped
// UpstreamError and must not return a loopback-marked context.
func TestApplyDispatchGuardBlocksByDefault(t *testing.T) {
	SetAllowPrivateProviderURLs(false)
	t.Setenv("ALLOW_PRIVATE_NETWORKING", "false")
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "")
	cfg := &providers.ProviderConfig{BaseURL: "http://10.0.0.5:1234/v1"}

	ctx, err := applyDispatchGuard(context.Background(), cfg)
	if err == nil {
		t.Fatal("applyDispatchGuard allowed a private URL with the setting off")
	}
	up, ok := err.(*UpstreamError)
	if !ok {
		t.Fatalf("error is %T, want *UpstreamError", err)
	}
	if up.StatusCode != 403 {
		t.Fatalf("status = %d, want 403", up.StatusCode)
	}
	if !strings.Contains(string(up.Body), "address_forbidden") {
		t.Fatalf("body %q does not carry the address_forbidden code", up.Body)
	}
	if IsLoopbackAllowed(ctx) {
		t.Fatal("blocked request returned a loopback-marked context")
	}
}

// TestApplyDispatchGuardMarksLoopbackWhenAllowed pins the other half of the
// hook: a permitted local URL comes back with the dialer marker, so the
// opt-in decision reaches the socket.
func TestApplyDispatchGuardMarksLoopbackWhenAllowed(t *testing.T) {
	SetAllowPrivateProviderURLs(true)
	defer SetAllowPrivateProviderURLs(false)
	cfg := &providers.ProviderConfig{BaseURL: "http://192.168.1.10:8000/v1"}

	ctx, err := applyDispatchGuard(context.Background(), cfg)
	if err != nil {
		t.Fatalf("applyDispatchGuard refused an opted-in private URL: %v", err)
	}
	if !IsLoopbackAllowed(ctx) {
		t.Fatal("allowed private request returned a context without the loopback marker")
	}
}

// TestAssertDispatchURLAllowedDefersDNS pins the hot-path contract: the guard
// is literal-only and never resolves a hostname. The resolver seam is swapped
// for one that answers EVERY hostname with a private address, so a guard that
// still called net.LookupIP would refuse the URL the way the resolving
// AssertPublicURL does; the guard must answer nil and leave the verdict on the
// resolved address to the socket dialer (newSafeDialerForProxy, whose
// ControlContext sees the address after DNS).
func TestAssertDispatchURLAllowedDefersDNS(t *testing.T) {
	orig := handlerutil.ResolvePublicAddrs
	handlerutil.ResolvePublicAddrs = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("192.168.1.10").To4()}, nil
	}
	defer func() { handlerutil.ResolvePublicAddrs = orig }()

	if err := AssertDispatchURLAllowed("http://private-host.example.test/v1", false); err != nil {
		t.Fatalf("AssertDispatchURLAllowed refused a hostname before resolution: %v", err)
	}
	// Same seam, same URL, resolving variant: still refuses. Proves the seam
	// is live and the difference is the literal-only guard, not the stub.
	if err := handlerutil.AssertPublicURL("http://private-host.example.test/v1"); err == nil {
		t.Fatal("AssertPublicURL did not refuse a hostname whose resolution is private")
	}
	// A hostname that would resolve into metadata space passes the guard too:
	// 169.254.0.0/16 is link-local, so the dialer refuses the resolved address
	// on every lane that carries the safe dialer.
	if err := AssertDispatchURLAllowed("http://metadata-host.example.test/latest/meta-data/", true); err != nil {
		t.Fatalf("AssertDispatchURLAllowed refused a hostname before resolution: %v", err)
	}
	// Literal checks stay intact without DNS: the seam never gets consulted
	// for an address the guard can judge as written.
	if err := AssertDispatchURLAllowed("http://127.0.0.1/v1", false); err == nil {
		t.Fatal("literal loopback stayed unblocked when DNS is deferred")
	}
}

// TestAssertDispatchURLAllowedNamesOptIn pins the refusal hint: a blocked
// loopback/private literal names the allowPrivateProviderURLs switch so a
// local-setup operator reads the fix in the error, while a metadata refusal —
// which the setting deliberately never excuses — does not.
func TestAssertDispatchURLAllowedNamesOptIn(t *testing.T) {
	err := AssertDispatchURLAllowed("http://127.0.0.1:11434/v1", false)
	if err == nil {
		t.Fatal("loopback refused neither by default nor hinted")
	}
	if !strings.Contains(err.Error(), "allowPrivateProviderURLs") {
		t.Fatalf("refusal %v does not name the allowPrivateProviderURLs switch", err)
	}
	if !strings.Contains(err.Error(), "ALLOW_PRIVATE_PROVIDER_URLS") {
		t.Fatalf("refusal %v does not name the env fallback", err)
	}

	err = AssertDispatchURLAllowed("http://169.254.169.254/latest/meta-data/", true)
	if err == nil {
		t.Fatal("metadata endpoint allowed even with opt-in")
	}
	if strings.Contains(err.Error(), "allowPrivateProviderURLs") {
		t.Fatalf("metadata refusal %v must not name the opt-in the setting cannot grant", err)
	}
}
