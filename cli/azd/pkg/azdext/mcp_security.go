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
	"sync"
)

// MCPSecurityPolicy validates URLs and file paths for MCP tool calls.
type MCPSecurityPolicy struct {
	mu               sync.RWMutex
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
	p.mu.Lock()
	defer p.mu.Unlock()
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

// BlockPrivateNetworks blocks RFC 1918 private networks, loopback, link-local,
// CGNAT (RFC 6598), deprecated IPv6 transition mechanisms (6to4, Teredo, NAT64),
// and IPv4-translated IPv6 addresses (RFC 2765).
func (p *MCPSecurityPolicy) BlockPrivateNetworks() *MCPSecurityPolicy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blockPrivate = true
	for _, cidr := range []string{
		"0.0.0.0/8",      // "this" network (reaches loopback on Linux/macOS)
		"10.0.0.0/8",     // RFC 1918 private
		"172.16.0.0/12",  // RFC 1918 private
		"192.168.0.0/16", // RFC 1918 private
		"127.0.0.0/8",    // loopback
		"100.64.0.0/10",  // RFC 6598 shared/CGNAT (internal in cloud environments)
		"169.254.0.0/16", // IPv4 link-local
		"::1/128",        // IPv6 loopback
		"::/128",         // IPv6 unspecified (reaches loopback)
		"fc00::/7",       // IPv6 unique local addresses (RFC 4193, equiv of RFC 1918)
		"fe80::/10",      // IPv6 link-local
		"2002::/16",      // 6to4 relay (deprecated RFC 7526; can embed private IPv4)
		"2001::/32",      // Teredo tunneling (deprecated; can embed private IPv4)
		"64:ff9b::/96",   // NAT64 well-known prefix (RFC 6052; embeds IPv4 in last 32 bits)
		"64:ff9b:1::/48", // NAT64 local-use prefix (RFC 8215; embeds IPv4 in last 32 bits)
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
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requireHTTPS = true
	return p
}

// RedactHeaders marks headers that should be blocked/redacted in requests.
func (p *MCPSecurityPolicy) RedactHeaders(headers ...string) *MCPSecurityPolicy {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, h := range headers {
		p.redactHeaders[strings.ToLower(h)] = true
	}
	return p
}

// ValidatePathsWithinBase restricts file paths to be within the given base directories.
func (p *MCPSecurityPolicy) ValidatePathsWithinBase(basePaths ...string) *MCPSecurityPolicy {
	p.mu.Lock()
	defer p.mu.Unlock()
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
	p.mu.RLock()
	defer p.mu.RUnlock()

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	host := u.Hostname()

	// Scheme validation - only allow http and https schemes.
	switch u.Scheme {
	case "https":
		// always allowed
	case "http":
		if p.requireHTTPS && !isLocalhostHost(host) {
			return fmt.Errorf("HTTPS required: %s", rawURL)
		}
	default:
		return fmt.Errorf("scheme not allowed: %q (only http and https are permitted)", u.Scheme)
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

	if p.blockPrivate {
		// Catch encoding variants (e.g., IPv4-compatible IPv6 like ::127.0.0.1)
		// that may not match CIDR entries due to byte-length mismatch.
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("blocked IP %s (private/loopback/link-local) for host %s", ip, originalHost)
		}

		// Handle encoding variants that Go's net.IP methods don't classify, by extracting
		// the embedded IPv4 address and re-checking it against all blocked ranges.
		if len(ip) == net.IPv6len && ip.To4() == nil {
			// IPv4-compatible (::x.x.x.x, RFC 4291 §2.5.5.1): first 12 bytes are zero.
			isV4Compatible := true
			for i := 0; i < 12; i++ {
				if ip[i] != 0 {
					isV4Compatible = false
					break
				}
			}
			if isV4Compatible && (ip[12] != 0 || ip[13] != 0 || ip[14] != 0 || ip[15] != 0) {
				v4 := net.IPv4(ip[12], ip[13], ip[14], ip[15])
				for _, cidr := range p.blockedCIDRs {
					if cidr.Contains(v4) {
						return fmt.Errorf("blocked IP %s (IPv4-compatible %s, CIDR %s) for host %s",
							ip, v4, cidr, originalHost)
					}
				}
				if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
					return fmt.Errorf("blocked IP %s (IPv4-compatible %s, private/loopback) for host %s",
						ip, v4, originalHost)
				}
			}

			// IPv4-translated (::ffff:0:x.x.x.x, RFC 2765 §4.2.1): bytes 0-7 zero,
			// bytes 8-9 = 0xFF 0xFF, bytes 10-11 = 0x00 0x00, bytes 12-15 = IPv4.
			// Distinct from IPv4-mapped (bytes 10-11 = 0xFF), so To4() returns nil.
			isV4Translated := ip[8] == 0xFF && ip[9] == 0xFF && ip[10] == 0x00 && ip[11] == 0x00
			if isV4Translated {
				for i := 0; i < 8; i++ {
					if ip[i] != 0 {
						isV4Translated = false
						break
					}
				}
			}
			if isV4Translated && (ip[12] != 0 || ip[13] != 0 || ip[14] != 0 || ip[15] != 0) {
				v4 := net.IPv4(ip[12], ip[13], ip[14], ip[15])
				for _, cidr := range p.blockedCIDRs {
					if cidr.Contains(v4) {
						return fmt.Errorf("blocked IP %s (IPv4-translated %s, CIDR %s) for host %s",
							ip, v4, cidr, originalHost)
					}
				}
				if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
					return fmt.Errorf("blocked IP %s (IPv4-translated %s, private/loopback) for host %s",
						ip, v4, originalHost)
				}
			}
		}
	}

	return nil
}

// CheckPath validates a file path against the security policy.
// Resolves symlinks and checks for directory traversal.
func (p *MCPSecurityPolicy) CheckPath(path string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

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
	p.mu.RLock()
	defer p.mu.RUnlock()
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
