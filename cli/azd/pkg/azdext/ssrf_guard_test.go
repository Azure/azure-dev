// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// SSRFGuard — metadata endpoint blocking
// ---------------------------------------------------------------------------

func TestSSRFGuard_BlocksMetadataEndpoints(t *testing.T) {
	guard := NewSSRFGuard().BlockMetadataEndpoints()

	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://fd00:ec2::254/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://100.100.100.200/latest/meta-data/",
		// Case variations
		"http://METADATA.GOOGLE.INTERNAL/computeMetadata/v1/",
	}
	for _, u := range blocked {
		if err := guard.Check(u); err == nil {
			t.Errorf("Check(%s) = nil, want blocked (metadata)", u)
		}
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — private network blocking
// ---------------------------------------------------------------------------

func TestSSRFGuard_BlocksPrivateIPs(t *testing.T) {
	guard := NewSSRFGuard().BlockPrivateNetworks()

	blocked := []struct {
		url  string
		desc string
	}{
		{"http://10.0.0.1/api", "RFC 1918 class A"},
		{"http://172.16.0.1/api", "RFC 1918 class B"},
		{"http://192.168.1.1/api", "RFC 1918 class C"},
		{"http://0.0.0.1/api", "0.0.0.0/8 'this' network"},
		{"http://100.64.0.1/api", "RFC 6598 CGNAT"},
		{"http://[fe80::1]/api", "IPv6 link-local"},
		{"http://[fd00::1]/api", "IPv6 unique local (fc00::/7)"},
		{"http://[fd12:3456:789a::1]/api", "IPv6 ULA in fd00::/8"},
		{"http://[::ffff:10.0.0.1]/api", "IPv4-mapped RFC 1918"},
		{"http://[::10.0.0.1]/api", "IPv4-compatible RFC 1918"},
		{"http://[2002:a00:1::]/api", "6to4 embedding 10.0.0.1"},
		{"http://[2001:0000::1]/api", "Teredo range"},
		{"http://[64:ff9b::a00:1]/api", "NAT64 well-known prefix"},
		{"http://[64:ff9b:1::a00:1]/api", "NAT64 local-use prefix"},
		{"http://[::ffff:0:a00:1]/api", "IPv4-translated RFC 1918 (RFC 2765)"},
	}
	for _, tc := range blocked {
		if err := guard.Check(tc.url); err == nil {
			t.Errorf("Check(%s) = nil, want blocked (%s)", tc.url, tc.desc)
		}
	}
}

func TestSSRFGuard_LocalhostExempt(t *testing.T) {
	guard := NewSSRFGuard().BlockPrivateNetworks()

	// Localhost/loopback addresses are exempt from private network blocking
	// to support local development workflows (API servers, proxies, etc.).
	exempt := []struct {
		url  string
		desc string
	}{
		{"http://127.0.0.1/api", "IPv4 loopback"},
		{"http://localhost:8080/api", "localhost hostname"},
		{"http://[::1]:8080/api", "IPv6 loopback"},
		{"http://[::ffff:127.0.0.1]/api", "IPv4-mapped loopback"},
	}
	for _, tc := range exempt {
		if err := guard.Check(tc.url); err != nil {
			t.Errorf("Check(%s) = %v, want nil (localhost exempt: %s)", tc.url, err, tc.desc)
		}
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — allows public URLs
// ---------------------------------------------------------------------------

func TestSSRFGuard_AllowsPublicURLs(t *testing.T) {
	guard := NewSSRFGuard().BlockPrivateNetworks().BlockMetadataEndpoints()
	// Mock DNS to avoid real network calls in tests.
	guard.lookupHost = func(host string) ([]string, error) {
		switch host {
		case "api.github.com":
			return []string{"140.82.121.6"}, nil
		case "example.com":
			return []string{"93.184.216.34"}, nil
		default:
			return nil, fmt.Errorf("unknown host: %s", host)
		}
	}

	allowed := []string{
		"https://api.github.com/repos",
		"https://example.com/data",
		"https://8.8.8.8/dns",
		"https://[2607:f8b0:4004:800::200e]/data", // public IPv6
	}
	for _, u := range allowed {
		if err := guard.Check(u); err != nil {
			t.Errorf("Check(%s) = %v, want nil", u, err)
		}
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — HTTPS enforcement
// ---------------------------------------------------------------------------

func TestSSRFGuard_EnforcesHTTPS(t *testing.T) {
	guard := NewSSRFGuard().RequireHTTPS()

	// HTTP to external host should be blocked.
	if err := guard.Check("http://example.com/api"); err == nil {
		t.Error("Check(http://example.com/api) = nil, want HTTPS required error")
	}

	// HTTP to localhost should be allowed.
	if err := guard.Check("http://localhost:8080/api"); err != nil {
		t.Errorf("Check(http://localhost:8080/api) = %v, want nil (localhost exempt)", err)
	}

	// HTTP to 127.0.0.1 should be allowed.
	if err := guard.Check("http://127.0.0.1:8080/api"); err != nil {
		t.Errorf("Check(http://127.0.0.1:8080/api) = %v, want nil (loopback exempt)", err)
	}

	// HTTP to [::1] should be allowed.
	if err := guard.Check("http://[::1]:8080/api"); err != nil {
		t.Errorf("Check(http://[::1]:8080/api) = %v, want nil (IPv6 loopback exempt)", err)
	}

	// HTTPS should always be allowed.
	if err := guard.Check("https://example.com/api"); err != nil {
		t.Errorf("Check(https://example.com/api) = %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — scheme blocking
// ---------------------------------------------------------------------------

func TestSSRFGuard_BlocksExoticSchemes(t *testing.T) {
	guard := NewSSRFGuard() // Even empty guard blocks non-HTTP schemes.

	blocked := []struct {
		url  string
		desc string
	}{
		{"ftp://example.com/file", "ftp"},
		{"gopher://example.com/path", "gopher"},
		{"file:///etc/passwd", "file"},
		{"//evil.com/path", "protocol-relative (empty scheme)"},
		{"ws://example.com/socket", "websocket"},
		{"wss://example.com/socket", "secure websocket"},
		{"ssh://example.com", "ssh"},
		{"telnet://example.com", "telnet"},
		{"ldap://example.com", "ldap"},
		{"dict://example.com", "dict"},
		{"jar://example.com", "jar"},
	}
	for _, tc := range blocked {
		err := guard.Check(tc.url)
		if err == nil {
			t.Errorf("Check(%s) = nil, want blocked (%s scheme)", tc.url, tc.desc)
			continue
		}
		var ssrfErr *SSRFError
		if !errors.As(err, &ssrfErr) {
			t.Errorf("Check(%s) returned %T, want *SSRFError", tc.url, err)
			continue
		}
		if ssrfErr.Reason != "scheme_blocked" {
			t.Errorf("Check(%s).Reason = %q, want %q", tc.url, ssrfErr.Reason, "scheme_blocked")
		}
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — DNS resolution
// ---------------------------------------------------------------------------

func TestSSRFGuard_DNSResolvesToBlockedIP(t *testing.T) {
	guard := NewSSRFGuard().BlockPrivateNetworks()
	guard.lookupHost = func(host string) ([]string, error) {
		return []string{"10.0.0.1"}, nil
	}

	err := guard.Check("http://evil.example.com/steal")
	if err == nil {
		t.Error("Check should block URL resolving to private IP via DNS")
	}

	var ssrfErr *SSRFError
	if !errors.As(err, &ssrfErr) {
		t.Fatalf("Check returned %T, want *SSRFError", err)
	}
	if ssrfErr.Reason != "blocked_ip" {
		t.Errorf("Reason = %q, want %q", ssrfErr.Reason, "blocked_ip")
	}
}

func TestSSRFGuard_DNSResolvesToBlockedHost(t *testing.T) {
	guard := NewSSRFGuard().BlockMetadataEndpoints()
	guard.lookupHost = func(host string) ([]string, error) {
		return []string{"169.254.169.254"}, nil
	}

	err := guard.Check("http://evil.example.com/steal")
	if err == nil {
		t.Error("Check should block URL resolving to metadata IP via DNS")
	}
}

func TestSSRFGuard_DNSFailureBlocksRequest(t *testing.T) {
	guard := NewSSRFGuard().BlockPrivateNetworks()
	guard.lookupHost = func(host string) ([]string, error) {
		return nil, fmt.Errorf("dns: NXDOMAIN")
	}

	err := guard.Check("http://evil.example.com/steal")
	if err == nil {
		t.Fatal("Check should block URL when DNS resolution fails (fail-closed)")
	}

	var ssrfErr *SSRFError
	if !errors.As(err, &ssrfErr) {
		t.Fatalf("Check returned %T, want *SSRFError", err)
	}
	if ssrfErr.Reason != "dns_failure" {
		t.Errorf("Reason = %q, want %q", ssrfErr.Reason, "dns_failure")
	}
	if !strings.Contains(ssrfErr.Detail, "fail-closed") {
		t.Errorf("Detail should mention fail-closed, got: %s", ssrfErr.Detail)
	}
}

func TestSSRFGuard_DNSMultipleAddresses(t *testing.T) {
	guard := NewSSRFGuard().BlockPrivateNetworks()
	guard.lookupHost = func(host string) ([]string, error) {
		// First address is public, second is private — should still block.
		return []string{"8.8.8.8", "192.168.1.1"}, nil
	}

	err := guard.Check("http://dual-homed.example.com/api")
	if err == nil {
		t.Error("Check should block when any resolved IP is private")
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — allowlist
// ---------------------------------------------------------------------------

func TestSSRFGuard_AllowHost(t *testing.T) {
	guard := NewSSRFGuard().
		BlockPrivateNetworks().
		BlockMetadataEndpoints().
		AllowHost("internal.corp.example.com")

	// The allowed host should bypass all checks.
	guard.lookupHost = func(host string) ([]string, error) {
		if host == "internal.corp.example.com" {
			return []string{"10.0.0.50"}, nil // would normally be blocked
		}
		return nil, fmt.Errorf("unknown host")
	}

	if err := guard.Check("http://internal.corp.example.com/api"); err != nil {
		t.Errorf("Check allowed host = %v, want nil", err)
	}

	// Non-allowed hosts should still be blocked.
	guard.lookupHost = func(host string) ([]string, error) {
		return []string{"10.0.0.50"}, nil
	}
	if err := guard.Check("http://not-allowed.example.com/api"); err == nil {
		t.Error("Check non-allowed host resolving to private IP = nil, want error")
	}
}

func TestSSRFGuard_AllowHostCaseInsensitive(t *testing.T) {
	guard := NewSSRFGuard().
		BlockMetadataEndpoints().
		AllowHost("Allowed.Example.COM")

	guard.lookupHost = func(host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}

	if err := guard.Check("http://allowed.example.com/api"); err != nil {
		t.Errorf("AllowHost should be case-insensitive, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — DefaultSSRFGuard preset
// ---------------------------------------------------------------------------

func TestDefaultSSRFGuard(t *testing.T) {
	guard := DefaultSSRFGuard()

	// Should block metadata.
	if err := guard.Check("http://169.254.169.254/metadata"); err == nil {
		t.Error("DefaultSSRFGuard should block metadata endpoint")
	}

	// Should block private IPs.
	if err := guard.Check("http://10.0.0.1/api"); err == nil {
		t.Error("DefaultSSRFGuard should block private IPs")
	}

	// Should require HTTPS.
	if err := guard.Check("http://example.com/api"); err == nil {
		t.Error("DefaultSSRFGuard should require HTTPS")
	}

	// Should allow HTTPS public URLs.
	if err := guard.Check("https://example.com/api"); err != nil {
		t.Errorf("DefaultSSRFGuard should allow HTTPS public URL, got: %v", err)
	}

	// Should allow HTTP to localhost.
	if err := guard.Check("http://localhost:8080/api"); err != nil {
		t.Errorf("DefaultSSRFGuard should allow HTTP to localhost, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — empty guard permissiveness
// ---------------------------------------------------------------------------

func TestSSRFGuard_EmptyGuardAllowsHTTP(t *testing.T) {
	guard := NewSSRFGuard()

	// Empty guard should allow HTTP and HTTPS but still block exotic schemes.
	if err := guard.Check("http://example.com/api"); err != nil {
		t.Errorf("empty guard should allow HTTP, got: %v", err)
	}
	if err := guard.Check("https://example.com/api"); err != nil {
		t.Errorf("empty guard should allow HTTPS, got: %v", err)
	}
	if err := guard.Check("ftp://example.com/file"); err == nil {
		t.Error("empty guard should still block FTP scheme")
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — edge cases
// ---------------------------------------------------------------------------

func TestSSRFGuard_InvalidURL(t *testing.T) {
	guard := NewSSRFGuard()

	err := guard.Check("://invalid")
	if err == nil {
		t.Error("Check(invalid URL) = nil, want error")
		return
	}
	var ssrfErr *SSRFError
	if !errors.As(err, &ssrfErr) {
		t.Errorf("Check returned %T, want *SSRFError", err)
		return
	}
	if ssrfErr.Reason != "scheme_blocked" && ssrfErr.Reason != "invalid_url" {
		t.Errorf("Reason = %q, want scheme_blocked or invalid_url", ssrfErr.Reason)
	}
}

func TestSSRFGuard_URLTruncation(t *testing.T) {
	guard := NewSSRFGuard().RequireHTTPS()

	// Create a very long URL.
	longURL := "http://" + strings.Repeat("a", 300) + ".com/path"
	err := guard.Check(longURL)
	if err == nil {
		t.Fatal("Check(long http URL) = nil, want HTTPS error")
	}

	var ssrfErr *SSRFError
	if !errors.As(err, &ssrfErr) {
		t.Fatalf("Check returned %T, want *SSRFError", err)
	}
	// URL should be truncated in the error to avoid log flooding.
	if len(ssrfErr.URL) > 210 {
		t.Errorf("SSRFError.URL should be truncated, got length %d", len(ssrfErr.URL))
	}
}

// ---------------------------------------------------------------------------
// SSRFGuard — concurrent safety
// ---------------------------------------------------------------------------

func TestSSRFGuard_ConcurrentCheck(t *testing.T) {
	guard := DefaultSSRFGuard()

	done := make(chan struct{})
	for range 100 {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = guard.Check("https://example.com/api")
			_ = guard.Check("http://10.0.0.1/api")
		}()
	}
	for range 100 {
		<-done
	}
}

// ---------------------------------------------------------------------------
// SSRFError
// ---------------------------------------------------------------------------

func TestSSRFError_ErrorMessage(t *testing.T) {
	err := &SSRFError{
		URL:    "http://evil.com/steal",
		Reason: "blocked_ip",
		Detail: "IP 10.0.0.1 is private",
	}

	msg := err.Error()
	if !strings.Contains(msg, "blocked_ip") {
		t.Errorf("error message should contain reason, got: %s", msg)
	}
	if !strings.Contains(msg, "evil.com") {
		t.Errorf("error message should contain URL, got: %s", msg)
	}
	if !strings.Contains(msg, "10.0.0.1") {
		t.Errorf("error message should contain detail, got: %s", msg)
	}
}

// ---------------------------------------------------------------------------
// IPv6 embedding extraction
// ---------------------------------------------------------------------------

func TestExtractIPv4Compatible(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantV4 string
	}{
		{"loopback", "::127.0.0.1", "127.0.0.1"},
		{"private", "::10.0.0.1", "10.0.0.1"},
		{"public", "::8.8.8.8", "8.8.8.8"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := parseIPv6(t, tc.input)
			v4 := extractIPv4Compatible(ip)
			if v4 == nil {
				t.Fatal("extractIPv4Compatible returned nil")
			}
			if !v4.Equal(parseIP(t, tc.wantV4)) {
				t.Errorf("extractIPv4Compatible(%s) = %s, want %s", tc.input, v4, tc.wantV4)
			}
		})
	}
}

func TestExtractIPv4Compatible_ReturnsNil(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"normal_ipv6", "2001:db8::1"},
		{"ipv4_mapped", "::ffff:10.0.0.1"}, // To4() != nil, so not pure IPv6
		{"all_zeros", "::"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := parseIPv6(t, tc.input)
			// IPv4-mapped addresses have To4() != nil and won't reach extractIPv4Compatible.
			// For testing the extraction function directly, skip those.
			if ip.To4() != nil {
				t.Skip("IPv4-mapped; To4() != nil")
			}
			v4 := extractIPv4Compatible(ip)
			if v4 != nil {
				t.Errorf("extractIPv4Compatible(%s) = %s, want nil", tc.input, v4)
			}
		})
	}
}

func TestExtractIPv4Translated(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantV4 string
	}{
		{"loopback", "::ffff:0:127.0.0.1", "127.0.0.1"},
		{"private", "::ffff:0:10.0.0.1", "10.0.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := parseIPv6(t, tc.input)
			if ip.To4() != nil {
				t.Skip("To4() != nil; not a pure IPv6")
			}
			v4 := extractIPv4Translated(ip)
			if v4 == nil {
				t.Fatal("extractIPv4Translated returned nil")
			}
			if !v4.Equal(parseIP(t, tc.wantV4)) {
				t.Errorf("extractIPv4Translated(%s) = %s, want %s", tc.input, v4, tc.wantV4)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func parseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("failed to parse IP: %s", s)
	}
	return ip
}

func parseIPv6(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("failed to parse IPv6: %s", s)
	}
	// Ensure we have a 16-byte representation.
	if len(ip) != net.IPv6len {
		ip = ip.To16()
	}
	return ip
}
