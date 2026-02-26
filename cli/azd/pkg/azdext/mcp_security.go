// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// MCPSecurityPolicy validates URLs and file paths for MCP tool calls.
type MCPSecurityPolicy struct {
	blockMetadata    bool
	blockPrivate     bool
	requireHTTPS     bool
	redactHeaders    map[string]bool
	allowedBasePaths []string
	blockedCIDRs     []*net.IPNet
	blockedHosts     map[string]bool
	// lookupHost is used for DNS resolution; override in tests.
	lookupHost func(string) ([]string, error)
}

// NewMCPSecurityPolicy creates an empty security policy.
func NewMCPSecurityPolicy() *MCPSecurityPolicy {
	return &MCPSecurityPolicy{
		redactHeaders: make(map[string]bool),
		blockedHosts:  make(map[string]bool),
		lookupHost:    net.LookupHost,
	}
}

// BlockMetadataEndpoints blocks cloud metadata service endpoints
// (169.254.169.254, fd00:ec2::254, metadata.google.internal, etc.)
func (p *MCPSecurityPolicy) BlockMetadataEndpoints() *MCPSecurityPolicy {
	p.blockMetadata = true
	for _, host := range []string{
		"169.254.169.254",
		"fd00:ec2::254",
		"metadata.google.internal",
		"100.100.100.200",
	} {
		p.blockedHosts[strings.ToLower(host)] = true
	}
	return p
}

// BlockPrivateNetworks blocks RFC 1918 private networks, loopback, and link-local.
func (p *MCPSecurityPolicy) BlockPrivateNetworks() *MCPSecurityPolicy {
	p.blockPrivate = true
	for _, cidr := range []string{
		"0.0.0.0/8",      // "this" network (reaches loopback on Linux/macOS)
		"10.0.0.0/8",     // RFC 1918 private
		"172.16.0.0/12",  // RFC 1918 private
		"192.168.0.0/16", // RFC 1918 private
		"127.0.0.0/8",    // loopback
		"::1/128",        // IPv6 loopback
		"::/128",         // IPv6 unspecified (reaches loopback)
		"fe80::/10",      // IPv6 link-local
		"169.254.0.0/16", // IPv4 link-local
	} {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			p.blockedCIDRs = append(p.blockedCIDRs, ipNet)
		}
	}
	return p
}

// RequireHTTPS requires HTTPS for all URLs except localhost/127.0.0.1.
func (p *MCPSecurityPolicy) RequireHTTPS() *MCPSecurityPolicy {
	p.requireHTTPS = true
	return p
}

// RedactHeaders marks headers that should be blocked/redacted in requests.
func (p *MCPSecurityPolicy) RedactHeaders(headers ...string) *MCPSecurityPolicy {
	for _, h := range headers {
		p.redactHeaders[strings.ToLower(h)] = true
	}
	return p
}

// ValidatePathsWithinBase restricts file paths to be within the given base directories.
func (p *MCPSecurityPolicy) ValidatePathsWithinBase(basePaths ...string) *MCPSecurityPolicy {
	p.allowedBasePaths = append(p.allowedBasePaths, basePaths...)
	return p
}

// isLocalhostHost returns true if the host is localhost or a loopback address.
func isLocalhostHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// CheckURL validates a URL against the security policy.
// Returns an error describing the violation, or nil if allowed.
func (p *MCPSecurityPolicy) CheckURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	host := u.Hostname()

	// Enforce HTTPS unless the host is localhost/loopback.
	if p.requireHTTPS && u.Scheme == "http" && !isLocalhostHost(host) {
		return fmt.Errorf("HTTPS required: %s", rawURL)
	}

	// Check if the host is directly blocked.
	if p.blockedHosts[strings.ToLower(host)] {
		return fmt.Errorf("blocked host: %s", host)
	}

	// If the host is an IP literal, check it directly against blocked CIDRs.
	if ip := net.ParseIP(host); ip != nil {
		if err := p.checkIP(ip, host); err != nil {
			return err
		}
	} else {
		// Resolve the hostname and check all resulting IPs.
		addrs, err := p.lookupHost(host)
		if err != nil {
			// Fail-closed: if DNS resolution fails, block the request.
			// This prevents SSRF bypasses via DNS rebinding or transient failures.
			return fmt.Errorf("DNS resolution failed for host %s: %w", host, err)
		}
		for _, addr := range addrs {
			if p.blockedHosts[strings.ToLower(addr)] {
				return fmt.Errorf("blocked host: %s (resolved from %s)", addr, host)
			}
			if ip := net.ParseIP(addr); ip != nil {
				if err := p.checkIP(ip, host); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (p *MCPSecurityPolicy) checkIP(ip net.IP, originalHost string) error {
	for _, cidr := range p.blockedCIDRs {
		if cidr.Contains(ip) {
			return fmt.Errorf("blocked IP %s (CIDR %s) for host %s", ip, cidr, originalHost)
		}
	}
	return nil
}

// CheckPath validates a file path against the security policy.
// Resolves symlinks and checks for directory traversal.
func (p *MCPSecurityPolicy) CheckPath(path string) error {
	if len(p.allowedBasePaths) == 0 {
		return nil
	}

	// Reject paths containing ".." before any cleaning to catch obvious traversal attempts.
	if strings.Contains(path, "..") {
		return fmt.Errorf("path traversal detected: %s", path)
	}

	cleaned := filepath.Clean(path)

	// Try to resolve symlinks; fall back to resolving the closest existing ancestor.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to resolve path: %w", err)
		}
		resolved = resolveExistingPrefix(cleaned)
	}

	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	for _, base := range p.allowedBasePaths {
		absBase, err := filepath.Abs(base)
		if err != nil {
			continue
		}
		// Resolve symlinks on the base path so it matches the resolved target path.
		if resolved, err := filepath.EvalSymlinks(absBase); err == nil {
			absBase = resolved
		}
		// Ensure the base path ends with a separator for proper prefix matching.
		if !strings.HasSuffix(absBase, string(filepath.Separator)) {
			absBase += string(filepath.Separator)
		}
		pathWithSep := absPath + string(filepath.Separator)
		baseWithoutSep := strings.TrimSuffix(absBase, string(filepath.Separator))
		if strings.HasPrefix(pathWithSep, absBase) || absPath == baseWithoutSep {
			return nil
		}
	}

	return fmt.Errorf("path %s is not within any allowed base directory", path)
}

// IsHeaderBlocked checks if a header name is in the redacted set.
// Returns true if the header should be blocked.
func (p *MCPSecurityPolicy) IsHeaderBlocked(header string) bool {
	return p.redactHeaders[strings.ToLower(header)]
}

// DefaultMCPSecurityPolicy returns a policy with metadata endpoints blocked,
// private networks blocked, HTTPS required, and common sensitive headers redacted.
func DefaultMCPSecurityPolicy() *MCPSecurityPolicy {
	return NewMCPSecurityPolicy().
		BlockMetadataEndpoints().
		BlockPrivateNetworks().
		RequireHTTPS().
		RedactHeaders("Authorization", "X-Api-Key", "Cookie", "Set-Cookie")
}

// resolveExistingPrefix resolves symlinks for the longest existing ancestor of
// a path and appends the remaining (non-existent) suffix. This handles cases
// like macOS where /var is a symlink to /private/var.
func resolveExistingPrefix(p string) string {
	dir := filepath.Dir(p)
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil {
		return filepath.Join(resolved, filepath.Base(p))
	}

	// Walk up until we find an existing ancestor.
	remaining := filepath.Base(p)
	current := dir
	for {
		parent := filepath.Dir(current)
		if parent == current {
			// Reached root without finding an existing directory.
			return p
		}
		remaining = filepath.Join(filepath.Base(current), remaining)
		current = parent
		resolved, err = filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, remaining)
		}
	}
}
