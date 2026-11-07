// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package handlerutil

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestAssertPublicURL_TrailingDot(t *testing.T) {
	if err := AssertPublicURL("http://localhost./"); err == nil {
		t.Error("localhost. with trailing dot should be blocked")
	}
	if err := AssertPublicURL("http://example.com./"); err != nil {
		t.Errorf("example.com. with trailing dot should be allowed (public), got %v", err)
	}
	if err := AssertPublicURL("http://foo.internal./"); err == nil {
		t.Error("foo.internal. with trailing dot should be blocked")
	}
	if err := AssertPublicURL("http://foo.local./"); err == nil {
		t.Error("foo.local. with trailing dot should be blocked")
	}
}

func TestAssertPublicURL_BlockedHostnames(t *testing.T) {
	for _, host := range []string{"http://localhost/", "http://ip6-localhost/", "http://ip6-loopback/"} {
		if err := AssertPublicURL(host); err == nil {
			t.Errorf("%s should be blocked", host)
		}
	}
}

func TestAssertPublicURL_BlockedSuffixes(t *testing.T) {
	for _, host := range []string{"http://foo.internal/", "http://bar.local/", "http://baz.localhost/"} {
		if err := AssertPublicURL(host); err == nil {
			t.Errorf("%s should be blocked", host)
		}
	}
}

func TestAssertPublicURL_PrivateIPv4(t *testing.T) {
	blocked := []string{
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://172.16.0.5/",
		"http://127.0.0.1/",
		"http://0.0.0.0/",
		"http://100.64.0.1/", // CGNAT
		"http://100.64.255.255/",
		"http://169.254.169.254/", // cloud metadata
	}
	for _, u := range blocked {
		if err := AssertPublicURL(u); err == nil {
			t.Errorf("%s should be blocked", u)
		}
	}
	// Public should pass
	if err := AssertPublicURL("https://api.openai.com/v1/chat/completions"); err != nil {
		t.Errorf("public should pass, got %v", err)
	}
	if err := AssertPublicURL("https://8.8.8.8/"); err != nil {
		t.Errorf("8.8.8.8 should pass, got %v", err)
	}
}

func TestAssertPublicURL_IPv6(t *testing.T) {
	blocked := []string{
		"http://[::1]/",
		"http://[::]/",
		"http://[fe80::1]/",
		"http://[fc00::1]/",
		"http://[fd00::1]/",
		"http://[::ffff:127.0.0.1]/",   // dotted IPv4-mapped
		"http://[::ffff:7f00:1]/",      // hex IPv4-mapped -> 127.0.0.1
		"http://[64:ff9b::127.0.0.1]/", // NAT64
		"http://[64:ff9b::7f00:1]/",    // NAT64 hex
		"http://[::192.168.1.1]/",      // IPv4-compatible
	}
	for _, u := range blocked {
		if err := AssertPublicURL(u); err == nil {
			t.Errorf("%s should be blocked (IPv6)", u)
		}
	}
	// Public IPv6 should pass (Google DNS)
	if err := AssertPublicURL("http://[2607:f8b0:4004:800::200e]/"); err != nil {
		t.Errorf("public IPv6 should pass, got %v", err)
	}
}

func TestParseIPv6ToGroups(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:7f00:1", true},
		{"64:ff9b::127.0.0.1", true},
		{"fe80::1", true},
		{"2001:db8::1", true},
		{"invalid:::", false},
		{"::ffff:999.999.999.999", false},
	}
	for _, tc := range tests {
		groups := parseIPv6ToGroups(tc.input)
		if tc.valid && groups == nil {
			t.Errorf("parseIPv6ToGroups(%q) should be valid", tc.input)
		}
		if !tc.valid && groups != nil {
			t.Errorf("parseIPv6ToGroups(%q) should be invalid, got %v", tc.input, groups)
		}
		if groups != nil && len(groups) != 8 {
			t.Errorf("expected 8 groups for %q, got %d", tc.input, len(groups))
		}
	}
}

func TestIsBlockedIpv6Groups(t *testing.T) {
	// Loopback
	if !isBlockedIpv6Groups(parseIPv6ToGroups("::1")) {
		t.Error("::1 should be blocked")
	}
	if isBlockedIpv6Groups(parseIPv6ToGroups("2001:db8::1")) {
		t.Error("2001:db8::1 should not be blocked")
	}
	// IPv4-mapped 127.0.0.1
	if !isBlockedIpv6Groups(parseIPv6ToGroups("::ffff:127.0.0.1")) {
		t.Error("::ffff:127.0.0.1 should be blocked")
	}
	if !isBlockedIpv6Groups(parseIPv6ToGroups("::ffff:7f00:1")) {
		t.Error("::ffff:7f00:1 should be blocked (hex)")
	}
	// Public
	if isBlockedIpv6Groups(parseIPv6ToGroups("2607:f8b0:4004:800::200e")) {
		t.Error("public IPv6 should not be blocked")
	}
}

func TestNormalizeHost(t *testing.T) {
	if got := normalizeHost("LocalHost."); got != "localhost" {
		t.Errorf("expected localhost, got %q", got)
	}
	if got := normalizeHost("EXAMPLE.COM..."); got != "example.com" {
		t.Errorf("expected example.com, got %q", got)
	}
}

func TestIsBlockedHost(t *testing.T) {
	if !isBlockedHost("localhost") {
		t.Error("localhost should be blocked")
	}
	if !isBlockedHost("foo.internal") {
		t.Error("foo.internal should be blocked")
	}
	if isBlockedHost("example.com") {
		t.Error("example.com should not be blocked")
	}
	if !isBlockedHost("10.0.0.1") {
		t.Error("10.0.0.1 should be blocked")
	}
	if isBlockedHost("8.8.8.8") {
		t.Error("8.8.8.8 should not be blocked")
	}
}

// TestAssertPublicURLLiteralsNeverResolves pins the hot-path contract: the
// literal variant judges the URL string only and never touches the resolver.
// The seam is replaced with one that fails the test if consulted.
func TestAssertPublicURLLiteralsNeverResolves(t *testing.T) {
	orig := ResolvePublicAddrs
	ResolvePublicAddrs = func(ctx context.Context, host string) ([]net.IP, error) {
		t.Errorf("ResolvePublicAddrs consulted for %q; AssertPublicURLLiterals must not resolve", host)
		return nil, errors.New("unexpected DNS lookup")
	}
	defer func() { ResolvePublicAddrs = orig }()

	// A plain hostname passes untouched: DNS is the dialer's job.
	if err := AssertPublicURLLiterals("http://api.openai.com/v1/chat/completions"); err != nil {
		t.Errorf("AssertPublicURLLiterals(public hostname) = %v, want nil", err)
	}
	if err := AssertPublicURLLiterals("http://private-host.example.test/"); err != nil {
		t.Errorf("AssertPublicURLLiterals(unknown hostname) = %v, want nil", err)
	}
	// Literals and static names are still judged without DNS.
	for _, u := range []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://localhost/",
		"http://foo.internal/",
		"http://[::1]/",
	} {
		if err := AssertPublicURLLiterals(u); err == nil {
			t.Errorf("AssertPublicURLLiterals(%q) = nil, want blocked", u)
		}
	}
}

// TestAssertPublicURLResolvesViaSeam pins the resolving half of AssertPublicURL:
// the hostname tail goes through ResolvePublicAddrs and every returned address
// is judged, so an injected private answer is refused and an injected public
// one is allowed.
func TestAssertPublicURLResolvesViaSeam(t *testing.T) {
	orig := ResolvePublicAddrs
	defer func() { ResolvePublicAddrs = orig }()

	ResolvePublicAddrs = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("192.168.1.10").To4()}, nil
	}
	if err := AssertPublicURL("http://private-resolving.example.test/"); err == nil {
		t.Error("AssertPublicURL allowed a hostname resolving to a private address")
	}

	ResolvePublicAddrs = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34").To4()}, nil
	}
	if err := AssertPublicURL("http://public-resolving.example.test/"); err != nil {
		t.Errorf("AssertPublicURL refused a hostname resolving to a public address: %v", err)
	}
}

// TestIsCloudMetadataURLLiterals pins the static metadata names the dispatch
// path relies on without DNS.
func TestIsCloudMetadataURLLiterals(t *testing.T) {
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://2852060926/latest/meta-data/",     // decimal literal
		"http://[fd00:ec2::254]/latest/api/token", // AWS IMDS over IPv6
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://x.metadata.google.internal/computeMetadata/v1/",
		"http://169.254.169.254.nip.io/latest/meta-data/",
		"http://100.100.100.200/latest/meta-data/", // Alibaba
		"http://168.63.129.16/machine/",            // Azure WireServer
	}
	for _, u := range blocked {
		if !IsCloudMetadataURLLiterals(u) {
			t.Errorf("IsCloudMetadataURLLiterals(%q) = false, want true", u)
		}
	}
	for _, u := range []string{
		"https://api.openai.com/v1",
		"http://93.184.216.34/v1",
		"http://metadata-host.example.test/",
	} {
		if IsCloudMetadataURLLiterals(u) {
			t.Errorf("IsCloudMetadataURLLiterals(%q) = true, want false", u)
		}
	}
}

// TestIsCloudMetadataURLResolvesViaSeam pins the resolving tail: a plain
// hostname whose injected answers include a metadata address is refused, which
// is the behavior the dashboard probes (non-hot-path callers) keep.
func TestIsCloudMetadataURLResolvesViaSeam(t *testing.T) {
	orig := ResolvePublicAddrs
	defer func() { ResolvePublicAddrs = orig }()

	ResolvePublicAddrs = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254").To4()}, nil
	}
	if !IsCloudMetadataURL(context.Background(), "http://metadata-resolving.example.test/") {
		t.Error("IsCloudMetadataURL missed a hostname resolving to the metadata service")
	}
}
