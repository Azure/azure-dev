// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPSecurityCheckURL_BlocksMetadataEndpoints(t *testing.T) {
	policy := NewMCPSecurityPolicy().BlockMetadataEndpoints()

	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://fd00:ec2::254/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://100.100.100.200/latest/meta-data/",
	}
	for _, u := range blocked {
		if err := policy.CheckURL(u); err == nil {
			t.Errorf("expected CheckURL to block metadata endpoint %s", u)
		}
	}
}

func TestMCPSecurityCheckURL_BlocksPrivateIPs(t *testing.T) {
	policy := NewMCPSecurityPolicy().BlockPrivateNetworks()

	blocked := []string{
		"http://10.0.0.1/api",
		"http://172.16.0.1/api",
		"http://192.168.1.1/api",
		"http://127.0.0.1/api",
		"http://0.0.0.1/api",             // 0.0.0.0/8 "this" network (reaches loopback on Linux/macOS)
		"http://100.64.0.1/api",          // RFC 6598 CGNAT (internal in cloud environments)
		"http://[::1]:8080/api",          // IPv6 loopback
		"http://[::]:80/api",             // IPv6 unspecified (reaches loopback)
		"http://[fe80::1]/api",           // IPv6 link-local
		"http://[fd00::1]/api",           // IPv6 unique local address (fc00::/7)
		"http://[fd12:3456:789a::1]/api", // IPv6 ULA in fd00::/8 range
		"http://[::ffff:127.0.0.1]/api",  // IPv4-mapped IPv6 loopback
		"http://[::ffff:10.0.0.1]/api",   // IPv4-mapped IPv6 RFC 1918
		"http://[::127.0.0.1]/api",       // IPv4-compatible IPv6 (deprecated, bypasses CIDR length match)
		"http://[::10.0.0.1]/api",        // IPv4-compatible IPv6 targeting RFC 1918
		"http://[2002:a00:1::]/api",      // 6to4 embedding 10.0.0.1 (deprecated RFC 7526)
		"http://[2001:0000::1]/api",      // Teredo range (deprecated; can embed private IPv4)
		"http://[64:ff9b::a00:1]/api",    // NAT64 well-known prefix (RFC 6052) embedding 10.0.0.1
		"http://[64:ff9b:1::a00:1]/api",  // NAT64 local-use prefix (RFC 8215) embedding 10.0.0.1
		"http://[::ffff:0:7f00:1]/api",   // IPv4-translated (RFC 2765) embedding 127.0.0.1
		"http://[::ffff:0:a00:1]/api",    // IPv4-translated (RFC 2765) embedding 10.0.0.1
	}
	for _, u := range blocked {
		if err := policy.CheckURL(u); err == nil {
			t.Errorf("expected CheckURL to block private IP in %s", u)
		}
	}
}

func TestMCPSecurityCheckURL_AllowsPublicURLs(t *testing.T) {
	policy := NewMCPSecurityPolicy().BlockPrivateNetworks().BlockMetadataEndpoints()

	allowed := []string{
		"https://api.github.com/repos",
		"https://example.com/data",
		"https://8.8.8.8/dns",
		"https://[2607:f8b0:4004:800::200e]/data", // public IPv6 (not in any blocked range)
	}
	for _, u := range allowed {
		if err := policy.CheckURL(u); err != nil {
			t.Errorf("expected CheckURL to allow public URL %s, got: %v", u, err)
		}
	}
}

func TestMCPSecurityCheckURL_EnforcesHTTPS(t *testing.T) {
	policy := NewMCPSecurityPolicy().RequireHTTPS()

	// HTTP to external host should be blocked.
	if err := policy.CheckURL("http://example.com/api"); err == nil {
		t.Error("expected CheckURL to block http://example.com/api")
	}

	// HTTP to localhost should be allowed.
	if err := policy.CheckURL("http://localhost:8080/api"); err != nil {
		t.Errorf("expected CheckURL to allow http to localhost, got: %v", err)
	}

	// HTTP to 127.0.0.1 should be allowed.
	if err := policy.CheckURL("http://127.0.0.1:8080/api"); err != nil {
		t.Errorf("expected CheckURL to allow http to 127.0.0.1, got: %v", err)
	}

	// HTTPS should always be allowed.
	if err := policy.CheckURL("https://example.com/api"); err != nil {
		t.Errorf("expected CheckURL to allow https, got: %v", err)
	}
}

func TestMCPSecurityCheckURL_DNSResolvesToBlockedIP(t *testing.T) {
	policy := NewMCPSecurityPolicy().BlockPrivateNetworks()
	// Override lookupHost to simulate DNS resolving to a private IP.
	policy.lookupHost = func(host string) ([]string, error) {
		return []string{"10.0.0.1"}, nil
	}

	if err := policy.CheckURL("http://evil.example.com/steal"); err == nil {
		t.Error("expected CheckURL to block URL resolving to private IP via DNS")
	}
}

func TestMCPSecurityCheckURL_DNSFailureBlocksRequest(t *testing.T) {
	policy := NewMCPSecurityPolicy().BlockPrivateNetworks()
	// Override lookupHost to simulate DNS failure.
	policy.lookupHost = func(host string) ([]string, error) {
		return nil, fmt.Errorf("dns: NXDOMAIN")
	}

	err := policy.CheckURL("http://evil.example.com/steal")
	if err == nil {
		t.Fatal("expected CheckURL to block URL when DNS resolution fails (fail-closed)")
	}
	if !strings.Contains(err.Error(), "DNS resolution failed") {
		t.Errorf("expected DNS resolution failure error, got: %v", err)
	}
}

func TestMCPSecurityCheckPath_BlocksTraversal(t *testing.T) {
	policy := NewMCPSecurityPolicy().ValidatePathsWithinBase("/safe/dir")

	traversal := []string{
		"/safe/dir/../../../etc/passwd",
		"/safe/dir/../../secret",
		"../../../etc/passwd",
	}
	for _, p := range traversal {
		if err := policy.CheckPath(p); err == nil {
			t.Errorf("expected CheckPath to block traversal path %s", p)
		}
	}
}

func TestMCPSecurityCheckPath_AllowsWithinBase(t *testing.T) {
	// Create a temporary directory structure for testing.
	base := t.TempDir()
	subdir := filepath.Join(base, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subdir, "file.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	policy := NewMCPSecurityPolicy().ValidatePathsWithinBase(base)

	if err := policy.CheckPath(testFile); err != nil {
		t.Errorf("expected CheckPath to allow path within base, got: %v", err)
	}

	if err := policy.CheckPath(subdir); err != nil {
		t.Errorf("expected CheckPath to allow subdirectory within base, got: %v", err)
	}
}

func TestMCPSecurityCheckPath_BlocksOutsideBase(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	policy := NewMCPSecurityPolicy().ValidatePathsWithinBase(base)

	if err := policy.CheckPath(outsideFile); err == nil {
		t.Errorf("expected CheckPath to block path outside base: %s", outsideFile)
	}
}

func TestMCPSecurityCheckPath_NonExistentFileWithinBase(t *testing.T) {
	base := t.TempDir()
	nonExistent := filepath.Join(base, "subdir", "new-file.txt")
	policy := NewMCPSecurityPolicy().ValidatePathsWithinBase(base)

	// Non-existent files within the base should be allowed (EvalSymlinks falls back).
	if err := policy.CheckPath(nonExistent); err != nil {
		t.Errorf("expected CheckPath to allow non-existent path within base, got: %v", err)
	}
}

func TestMCPSecurityCheckPath_ExactBaseMatch(t *testing.T) {
	base := t.TempDir()
	policy := NewMCPSecurityPolicy().ValidatePathsWithinBase(base)

	// The base directory itself should be allowed.
	if err := policy.CheckPath(base); err != nil {
		t.Errorf("expected CheckPath to allow exact base directory, got: %v", err)
	}
}

func TestMCPSecurityCheckPath_NoBasePathsAllowsAll(t *testing.T) {
	policy := NewMCPSecurityPolicy()

	if err := policy.CheckPath("/any/path/at/all"); err != nil {
		t.Errorf("expected CheckPath with no base paths to allow any path, got: %v", err)
	}
}

func TestMCPSecurityIsHeaderBlocked(t *testing.T) {
	policy := NewMCPSecurityPolicy().RedactHeaders("Authorization", "X-Api-Key")

	tests := []struct {
		header  string
		blocked bool
	}{
		{"Authorization", true},
		{"authorization", true},
		{"AUTHORIZATION", true},
		{"X-Api-Key", true},
		{"x-api-key", true},
		{"Content-Type", false},
		{"Accept", false},
	}

	for _, tc := range tests {
		got := policy.IsHeaderBlocked(tc.header)
		if got != tc.blocked {
			t.Errorf("IsHeaderBlocked(%q) = %v, want %v", tc.header, got, tc.blocked)
		}
	}
}

func TestMCPSecurityCheckURL_BlocksExoticSchemes(t *testing.T) {
	policy := NewMCPSecurityPolicy().RequireHTTPS()

	blocked := []struct {
		url  string
		desc string
	}{
		{"ftp://example.com/file", "ftp scheme"},
		{"gopher://example.com/path", "gopher scheme"},
		{"file:///etc/passwd", "file scheme"},
		{"//evil.com/path", "protocol-relative (empty scheme)"},
		{"ws://example.com/socket", "ws scheme"},
		{"wss://example.com/socket", "wss scheme"},
		{"ssh://example.com", "ssh scheme"},
		{"telnet://example.com", "telnet scheme"},
		{"ldap://example.com", "ldap scheme"},
		{"dict://example.com", "dict scheme"},
	}
	for _, tc := range blocked {
		if err := policy.CheckURL(tc.url); err == nil {
			t.Errorf("expected CheckURL to block %s (%s)", tc.url, tc.desc)
		}
	}

	// Even without requireHTTPS, exotic schemes must be blocked.
	permissive := NewMCPSecurityPolicy()
	for _, tc := range blocked[:3] { // ftp, gopher, file
		if err := permissive.CheckURL(tc.url); err == nil {
			t.Errorf("expected CheckURL (no requireHTTPS) to block %s (%s)", tc.url, tc.desc)
		}
	}

	// http and https must still be allowed when requireHTTPS is off.
	if err := permissive.CheckURL("http://example.com/api"); err != nil {
		t.Errorf("expected permissive policy to allow http, got: %v", err)
	}
	if err := permissive.CheckURL("https://example.com/api"); err != nil {
		t.Errorf("expected permissive policy to allow https, got: %v", err)
	}
}

func TestMCPSecurityDefaultPolicy(t *testing.T) {
	policy := DefaultMCPSecurityPolicy()

	// Should block metadata endpoints.
	if err := policy.CheckURL("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Error("default policy should block metadata endpoint")
	}

	// Should block private IPs.
	if err := policy.CheckURL("http://10.0.0.1/api"); err == nil {
		t.Error("default policy should block private IPs")
	}

	// Should require HTTPS.
	if err := policy.CheckURL("http://example.com/api"); err == nil {
		t.Error("default policy should require HTTPS")
	}

	// Should allow HTTPS public URLs.
	if err := policy.CheckURL("https://example.com/api"); err != nil {
		t.Errorf("default policy should allow HTTPS public URL, got: %v", err)
	}

	// Should redact sensitive headers.
	for _, h := range []string{"Authorization", "X-Api-Key", "Cookie", "Set-Cookie"} {
		if !policy.IsHeaderBlocked(h) {
			t.Errorf("default policy should block header %s", h)
		}
	}
}

func TestMCPSecurityFluentBuilder(t *testing.T) {
	// Verify the fluent builder pattern works by chaining all methods.
	policy := NewMCPSecurityPolicy().
		BlockMetadataEndpoints().
		BlockPrivateNetworks().
		RequireHTTPS().
		RedactHeaders("Authorization").
		ValidatePathsWithinBase("/tmp")

	require.NotNil(t, policy, "fluent builder should return non-nil policy")
	require.True(t, policy.blockMetadata, "blockMetadata should be true")
	require.True(t, policy.blockPrivate, "blockPrivate should be true")
	require.True(t, policy.requireHTTPS, "requireHTTPS should be true")
	require.True(t, policy.IsHeaderBlocked("Authorization"), "Authorization should be blocked")
	require.Len(t, policy.allowedBasePaths, 1, "expected 1 base path")
}

func TestSSRFSafeRedirect_SchemeDowngrade(t *testing.T) {
	t.Parallel()

	// Simulate HTTPS → HTTP redirect (credential leak vector).
	via := []*http.Request{
		{URL: &url.URL{Scheme: "https", Host: "example.com", Path: "/api"}},
	}
	req := &http.Request{
		URL: &url.URL{Scheme: "http", Host: "example.com", Path: "/api"},
	}

	err := SSRFSafeRedirect(req, via)
	if err == nil {
		t.Fatal("expected error for HTTPS → HTTP redirect (credential protection)")
	}
	if !strings.Contains(err.Error(), "credential protection") {
		t.Errorf("error = %q, want mention of credential protection", err.Error())
	}
}

func TestSSRFSafeRedirect_HTTPToHTTPAllowed(t *testing.T) {
	t.Parallel()

	// HTTP → HTTP redirect (no downgrade) should be allowed.
	via := []*http.Request{
		{URL: &url.URL{Scheme: "http", Host: "example.com", Path: "/api"}},
	}
	req := &http.Request{
		URL: &url.URL{Scheme: "http", Host: "example.com", Path: "/other"},
	}

	err := SSRFSafeRedirect(req, via)
	if err != nil {
		t.Errorf("HTTP → HTTP redirect should be allowed, got: %v", err)
	}
}

func TestSSRFSafeRedirect_LocalhostHostname(t *testing.T) {
	t.Parallel()

	// Redirect to "localhost" hostname should be blocked.
	req := &http.Request{
		URL: &url.URL{Scheme: "http", Host: "localhost:8080", Path: "/steal"},
	}

	err := SSRFSafeRedirect(req, nil)
	if err == nil {
		t.Fatal("expected error for redirect to localhost hostname")
	}
	if !strings.Contains(err.Error(), "localhost") {
		t.Errorf("error = %q, want mention of localhost", err.Error())
	}
}

func TestSSRFSafeRedirect_IPv4CompatiblePrivate(t *testing.T) {
	t.Parallel()

	// Redirect to IPv4-compatible IPv6 embedding private IP.
	req := &http.Request{
		URL: &url.URL{Scheme: "http", Host: "[::10.0.0.1]", Path: "/steal"},
	}

	err := SSRFSafeRedirect(req, nil)
	if err == nil {
		t.Fatal("expected error for redirect to IPv4-compatible private address")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Errorf("error = %q, want mention of SSRF", err.Error())
	}
}

func TestSSRFSafeRedirect_HostnameResolvesPrivateBlocked(t *testing.T) {
	req := &http.Request{
		URL: &url.URL{Scheme: "https", Host: "example.test", Path: "/next"},
	}

	err := ssrfSafeRedirect(req, nil, func(host string) ([]string, error) {
		return []string{"10.0.0.10"}, nil
	})
	if err == nil {
		t.Fatal("expected error for redirect hostname resolving to private IP")
	}
	if !strings.Contains(err.Error(), "resolved to private/loopback") {
		t.Errorf("error = %q, want mention of resolved private/loopback", err.Error())
	}
}

func TestSSRFSafeRedirect_HostnameDNSFailureBlocked(t *testing.T) {
	req := &http.Request{
		URL: &url.URL{Scheme: "https", Host: "example.test", Path: "/next"},
	}

	err := ssrfSafeRedirect(req, nil, func(host string) ([]string, error) {
		return nil, fmt.Errorf("dns unavailable")
	})
	if err == nil {
		t.Fatal("expected error for redirect hostname DNS failure")
	}
	if !strings.Contains(err.Error(), "DNS resolution failed") {
		t.Errorf("error = %q, want mention of DNS resolution failed", err.Error())
	}
}

func TestMCPSecurityOnBlocked_URLCallback(t *testing.T) {
	t.Parallel()

	var (
		gotAction string
		gotDetail string
		callCount int
	)

	policy := NewMCPSecurityPolicy().
		RequireHTTPS().
		OnBlocked(func(action, detail string) {
			gotAction = action
			gotDetail = detail
			callCount++
		})

	// This should trigger the callback: HTTP to non-localhost host.
	err := policy.CheckURL("http://example.com/api")
	if err == nil {
		t.Fatal("expected error for HTTP URL with HTTPS required")
	}

	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}
	if gotAction != "url_blocked" {
		t.Errorf("action = %q, want %q", gotAction, "url_blocked")
	}
	if !strings.Contains(gotDetail, "HTTPS required") {
		t.Errorf("detail = %q, want to contain %q", gotDetail, "HTTPS required")
	}
}

func TestMCPSecurityOnBlocked_PathCallback(t *testing.T) {
	t.Parallel()

	var gotAction string

	base := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	policy := NewMCPSecurityPolicy().
		ValidatePathsWithinBase(base).
		OnBlocked(func(action, detail string) {
			gotAction = action
		})

	err := policy.CheckPath(outsideFile)
	if err == nil {
		t.Fatal("expected error for path outside base")
	}

	if gotAction != "path_blocked" {
		t.Errorf("action = %q, want %q", gotAction, "path_blocked")
	}
}

func TestExtractEmbeddedIPv4(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ip     net.IP
		wantV4 net.IP
	}{
		{
			name:   "IPv4-compatible private",
			ip:     net.ParseIP("::10.0.0.1"),
			wantV4: net.IPv4(10, 0, 0, 1),
		},
		{
			name:   "IPv4-compatible loopback",
			ip:     net.ParseIP("::127.0.0.1"),
			wantV4: net.IPv4(127, 0, 0, 1),
		},
		{
			name:   "IPv4-translated private",
			ip:     net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0, 0, 10, 0, 0, 1},
			wantV4: net.IPv4(10, 0, 0, 1),
		},
		{
			name:   "IPv4-mapped (handled by To4)",
			ip:     net.ParseIP("::ffff:10.0.0.1"),
			wantV4: nil, // To4() != nil, so extractEmbeddedIPv4 returns nil
		},
		{
			name:   "public IPv6",
			ip:     net.ParseIP("2607:f8b0:4004:800::200e"),
			wantV4: nil,
		},
		{
			name:   "pure IPv4",
			ip:     net.ParseIP("10.0.0.1"),
			wantV4: nil, // len != IPv6len, returns nil
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := extractEmbeddedIPv4(tt.ip)
			if tt.wantV4 == nil {
				if got != nil {
					t.Errorf("extractEmbeddedIPv4(%s) = %s, want nil", tt.ip, got)
				}
			} else {
				if got == nil {
					t.Errorf("extractEmbeddedIPv4(%s) = nil, want %s", tt.ip, tt.wantV4)
				} else if !got.Equal(tt.wantV4) {
					t.Errorf("extractEmbeddedIPv4(%s) = %s, want %s", tt.ip, got, tt.wantV4)
				}
			}
		})
	}
}
