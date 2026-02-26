// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"http://0.0.0.1/api",         // 0.0.0.0/8 "this" network (reaches loopback on Linux/macOS)
		"http://[::1]:8080/api",       // IPv6 loopback
		"http://[::]:80/api",          // IPv6 unspecified (reaches loopback)
		"http://[fe80::1]/api",        // IPv6 link-local
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

	if policy == nil {
		t.Fatal("fluent builder should return non-nil policy")
	}

	if !policy.blockMetadata {
		t.Error("blockMetadata should be true")
	}
	if !policy.blockPrivate {
		t.Error("blockPrivate should be true")
	}
	if !policy.requireHTTPS {
		t.Error("requireHTTPS should be true")
	}
	if !policy.IsHeaderBlocked("Authorization") {
		t.Error("Authorization should be blocked")
	}
	if len(policy.allowedBasePaths) != 1 {
		t.Errorf("expected 1 base path, got %d", len(policy.allowedBasePaths))
	}
}
