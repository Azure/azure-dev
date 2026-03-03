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
	// lookupHost is used for DNS resolution; override in tests.
	lookupHost func(string) ([]string, error)
	// onBlocked is an optional callback invoked when a URL or path is blocked.
	// Parameters: action ("url_blocked", "path_blocked"),
	// detail (human-readable explanation). Safe for concurrent use.
	onBlocked func(action, detail string)
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

// OnBlocked registers a callback that is invoked whenever a URL or path is
// blocked by the security policy. This enables security audit
// logging without coupling the policy to a specific logging framework.
//
// The callback receives an action tag ("url_blocked", "path_blocked")
// and a human-readable detail string. It must be safe
// for concurrent invocation.
func (p *MCPSecurityPolicy) OnBlocked(fn func(action, detail string)) *MCPSecurityPolicy {
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
	fn := p.onBlocked
	err := p.checkURLCore(rawURL)
	p.mu.RUnlock()

	if fn != nil && err != nil {
		fn("url_blocked", err.Error())
	}

	return err
}

// checkURLCore performs URL validation without acquiring the lock or invoking
// the onBlocked callback. Callers must hold p.mu (at least RLock).
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
			return fmt.Errorf("HTTPS required: %s", redactSecurityURL(rawURL))
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

func redactSecurityURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
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

		// Handle encoding variants that Go's net.IP methods don't classify,
		// by extracting the embedded IPv4 and re-checking it.
		if v4 := extractEmbeddedIPv4(ip); v4 != nil {
			for _, cidr := range p.blockedCIDRs {
				if cidr.Contains(v4) {
					return fmt.Errorf("blocked IP %s (embedded %s, CIDR %s) for host %s",
						ip, v4, cidr, originalHost)
				}
			}
			if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
				return fmt.Errorf("blocked IP %s (embedded %s, private/loopback) for host %s",
					ip, v4, originalHost)
			}
		}
	}

	return nil
}

// CheckPath validates a file path against the security policy.
// Resolves symlinks and checks for directory traversal.
//
// Security note (TOCTOU): There is an inherent time-of-check to time-of-use
// gap between the symlink resolution performed here and the caller's
// subsequent file operation. An adversary with write access to the filesystem
// could create or modify a symlink between the check and the use. This is a
// fundamental limitation of path-based validation on POSIX systems.
//
// Mitigations callers should consider:
//   - Use O_NOFOLLOW when opening files after validation (prevents symlink
//     following at the final component).
//   - Use file-descriptor-based approaches (openat2 with RESOLVE_BENEATH on
//     Linux 5.6+) where possible.
//   - Avoid writing to directories that untrusted users can modify.
//   - Consider validating the opened fd's path post-open via /proc/self/fd/N
//     or fstat.
func (p *MCPSecurityPolicy) CheckPath(path string) error {
	p.mu.RLock()
	fn := p.onBlocked
	err := p.checkPathCore(path)
	p.mu.RUnlock()

	if fn != nil && err != nil {
		fn("path_blocked", err.Error())
	}

	return err
}

// checkPathCore performs path validation without acquiring the lock or invoking
// the onBlocked callback. Callers must hold p.mu (at least RLock).
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

// ---------------------------------------------------------------------------
// Redirect SSRF protection
// ---------------------------------------------------------------------------

// redirectBlockedHosts lists cloud metadata service endpoints that must never
// be the target of an HTTP redirect.
var redirectBlockedHosts = map[string]bool{
	"169.254.169.254":          true,
	"fd00:ec2::254":            true,
	"metadata.google.internal": true,
	"100.100.100.200":          true,
}

// SSRFSafeRedirect is an [http.Client] CheckRedirect function that blocks
// redirects to private/loopback IP literals, hostnames that resolve to private
// networks, and cloud metadata endpoints. It prevents
// redirect-based SSRF attacks where an attacker-controlled URL redirects to
// an internal service.
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
	// Go's net/http preserves headers on same-host redirects regardless
	// of scheme change.
	if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf(
			"redirect from HTTPS to %s blocked (credential protection)", req.URL.Scheme)
	}

	host := req.URL.Hostname()

	// Block redirects to known metadata endpoints.
	if redirectBlockedHosts[strings.ToLower(host)] {
		return fmt.Errorf("redirect to metadata endpoint %s blocked (SSRF protection)", host)
	}

	// Block redirects to localhost hostnames (e.g. "localhost",
	// "127.0.0.1") regardless of how they are spelled, preventing
	// hostname-based SSRF bypasses of the IP-literal checks below.
	if isLocalhostHost(host) {
		return fmt.Errorf("redirect to localhost %s blocked (SSRF protection)", host)
	}

	// Block redirects to private/loopback IP addresses, including
	// IPv4-compatible and IPv4-translated IPv6 encoding variants
	// that bypass Go's IsPrivate()/IsLoopback() classification.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("redirect to private/loopback IP %s blocked (SSRF protection)", ip)
		}

		// Check IPv6 encoding variants (IPv4-compatible, IPv4-translated)
		// that embed private IPv4 addresses but aren't caught by Go's
		// net.IP classifier methods.
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
// (::x.x.x.x, RFC 4291 §2.5.5.1) or IPv4-translated (::ffff:0:x.x.x.x,
// RFC 2765 §4.2.1) IPv6 encodings. Returns nil if the address is not one of
// these encoding variants.
//
// This handles addresses that Go's net.IP.To4() does not classify as IPv4
// (To4 returns nil for these), which means Go's IsPrivate()/IsLoopback()
// methods also return false for them.
func extractEmbeddedIPv4(ip net.IP) net.IP {
	if len(ip) != net.IPv6len || ip.To4() != nil {
		return nil // Not a pure IPv6 address or already handled as IPv4-mapped
	}

	// Check if last 4 bytes are non-zero (otherwise it's just :: which is
	// already handled by IsUnspecified).
	if ip[12] == 0 && ip[13] == 0 && ip[14] == 0 && ip[15] == 0 {
		return nil
	}

	// IPv4-compatible (::x.x.x.x): first 12 bytes are zero.
	isV4Compatible := true
	for i := 0; i < 12; i++ {
		if ip[i] != 0 {
			isV4Compatible = false
			break
		}
	}
	if isV4Compatible {
		return net.IPv4(ip[12], ip[13], ip[14], ip[15])
	}

	// IPv4-translated (::ffff:0:x.x.x.x, RFC 2765): bytes 0-7 zero,
	// bytes 8-9 = 0xFF 0xFF, bytes 10-11 = 0x00 0x00, bytes 12-15 = IPv4.
	// Distinct from IPv4-mapped (bytes 10-11 = 0xFF), so To4() returns nil.
	if ip[8] == 0xFF && ip[9] == 0xFF && ip[10] == 0x00 && ip[11] == 0x00 {
		allZero := true
		for i := 0; i < 8; i++ {
			if ip[i] != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			return net.IPv4(ip[12], ip[13], ip[14], ip[15])
		}
	}

	return nil
}
