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
	// onBlocked is invoked whenever a URL or path is blocked, for audit logging.
	onBlocked func(violation string)
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
	for _, host := range ssrfMetadataHosts {
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
	for _, cidr := range ssrfBlockedCIDRs {
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

// OnBlocked registers a callback invoked whenever a URL or path check fails.
// The callback receives a human-readable description of the violation.
// This is intended for audit logging; the callback must not block.
func (p *MCPSecurityPolicy) OnBlocked(fn func(violation string)) *MCPSecurityPolicy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onBlocked = fn
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
	err := p.checkURLCore(rawURL)
	onBlocked := p.onBlocked
	p.mu.RUnlock()

	if err != nil && onBlocked != nil {
		onBlocked(err.Error())
	}

	return err
}

func (p *MCPSecurityPolicy) checkURLCore(rawURL string) error {
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
	if _, detail, blocked := ssrfCheckIP(ip, originalHost, p.blockedCIDRs, p.blockPrivate); blocked {
		return fmt.Errorf("%s", detail)
	}

	return nil
}

// CheckPath validates a file path against the security policy.
// Resolves symlinks and checks for directory traversal.
//
// SECURITY NOTE (TOCTOU): This check is inherently susceptible to
// time-of-check-to-time-of-use races — the filesystem state may change between
// the validation here and the actual file access by the caller. Callers that
// operate in adversarial environments (e.g., shared file systems) should open
// the file immediately after validation and re-verify the resolved path via
// /proc/self/fd or fstat before processing.
func (p *MCPSecurityPolicy) CheckPath(path string) error {
	p.mu.RLock()
	err := p.checkPathCore(path)
	onBlocked := p.onBlocked
	p.mu.RUnlock()

	if err != nil && onBlocked != nil {
		onBlocked(err.Error())
	}

	return err
}

func (p *MCPSecurityPolicy) checkPathCore(path string) error {
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

// redirectBlockedHosts lists hostnames that HTTP redirects must never follow.
// This covers cloud metadata services that attackers commonly target via
// redirect-based SSRF (the initial request hits an allowed host which 302s
// to the metadata endpoint).
var redirectBlockedHosts = map[string]bool{
	"169.254.169.254":          true,
	"fd00:ec2::254":            true,
	"metadata.google.internal": true,
	"100.100.100.200":          true,
}

// SSRFSafeRedirect is an [http.Client] CheckRedirect function that blocks
// redirects to private/loopback IP literals, hostnames that resolve to private
// networks, and cloud metadata endpoints. It prevents redirect-based SSRF
// attacks where an attacker-controlled URL redirects to an internal service.
//
// Usage:
//
//	client := &http.Client{CheckRedirect: azdext.SSRFSafeRedirect}
func SSRFSafeRedirect(req *http.Request, via []*http.Request) error {
	return ssrfSafeRedirect(req, via, net.LookupHost)
}

func ssrfSafeRedirect(req *http.Request, via []*http.Request, lookupHost func(string) ([]string, error)) error {
	const maxRedirects = 10
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}

	// Block HTTPS → HTTP scheme downgrades to prevent leaking
	// Authorization headers (including Bearer tokens) in cleartext.
	if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf(
			"redirect from HTTPS to %s blocked (credential protection)", req.URL.Scheme)
	}

	host := req.URL.Hostname()

	// Block redirects to known metadata endpoints.
	if redirectBlockedHosts[strings.ToLower(host)] {
		return fmt.Errorf("redirect to metadata endpoint %s blocked (SSRF protection)", host)
	}

	// Block redirects to localhost hostnames.
	if isLocalhostHost(host) {
		return fmt.Errorf("redirect to localhost %s blocked (SSRF protection)", host)
	}

	// Block redirects to private/loopback IP addresses, including
	// IPv6 encoding variants that embed private IPv4 addresses.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("redirect to private/loopback IP %s blocked (SSRF protection)", ip)
		}

		if err := checkIPEncodingVariants(ip, host); err != nil {
			return err
		}
	}

	// Resolve hostnames and block redirects to private/loopback resolved IPs.
	ips, err := lookupHost(host)
	if err != nil {
		return fmt.Errorf("redirect host %s DNS resolution failed (SSRF protection): %w", host, err)
	}
	for _, rawIP := range ips {
		ip := net.ParseIP(rawIP)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("redirect host %s resolved to private/loopback IP %s blocked (SSRF protection)", host, ip)
		}
		if err := checkIPEncodingVariants(ip, host); err != nil {
			return err
		}
	}

	return nil
}

// checkIPEncodingVariants detects IPv4-compatible (::x.x.x.x) and
// IPv4-translated (::ffff:0:x.x.x.x) IPv6 addresses that embed
// private IPv4 addresses but bypass Go's IsPrivate()/IsLoopback().
func checkIPEncodingVariants(ip net.IP, originalHost string) error {
	v4 := extractEmbeddedIPv4(ip)
	if v4 == nil {
		return nil
	}

	if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
		return fmt.Errorf(
			"redirect to embedded IPv4 address %s (embedded %s) blocked (SSRF protection)",
			ip, v4)
	}

	return nil
}

// extractEmbeddedIPv4 returns the embedded IPv4 address from IPv4-compatible
// (::x.x.x.x) or IPv4-translated (::ffff:0:x.x.x.x) IPv6 encodings.
// Returns nil if the address is not one of these encoding variants.
func extractEmbeddedIPv4(ip net.IP) net.IP {
	if len(ip) != net.IPv6len || ip.To4() != nil {
		return nil
	}

	if v4 := extractIPv4Compatible(ip); v4 != nil {
		return v4
	}
	return extractIPv4Translated(ip)
}
