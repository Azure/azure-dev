// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
)

// SSRFGuard validates URLs against Server-Side Request Forgery (SSRF) attack
// patterns. It provides standalone SSRF protection for extension authors who
// need URL validation outside of MCP contexts.
//
// SSRFGuard uses a fluent builder pattern for configuration:
//
//	guard := azdext.NewSSRFGuard().
//	    BlockMetadataEndpoints().
//	    BlockPrivateNetworks().
//	    RequireHTTPS()
//
//	if err := guard.Check("http://169.254.169.254/metadata"); err != nil {
//	    // blocked: cloud metadata endpoint
//	}
//
// Use [DefaultSSRFGuard] for a preset configuration that blocks metadata
// endpoints, private networks, and requires HTTPS.
//
// SSRFGuard is safe for concurrent use from multiple goroutines.
type SSRFGuard struct {
	mu            sync.RWMutex
	blockMetadata bool
	blockPrivate  bool
	requireHTTPS  bool
	blockedCIDRs  []*net.IPNet
	blockedHosts  map[string]bool
	allowedHosts  map[string]bool
	// lookupHost is used for DNS resolution; override in tests.
	lookupHost func(string) ([]string, error)
	// onBlocked is an optional callback invoked when a URL is blocked.
	// Parameters: reason (machine-readable tag), detail (human-readable).
	onBlocked func(reason, detail string)
}

// SSRFError describes why a URL was rejected by the [SSRFGuard].
type SSRFError struct {
	// URL is the rejected URL (or a sanitized representation).
	URL string

	// Reason is a machine-readable tag for the violation type.
	// Values: "blocked_host", "blocked_ip", "private_network",
	// "metadata_endpoint", "dns_failure", "https_required",
	// "invalid_url", "scheme_blocked".
	Reason string

	// Detail is a human-readable explanation.
	Detail string
}

func (e *SSRFError) Error() string {
	return fmt.Sprintf("azdext.SSRFGuard: %s: %s (url=%s)", e.Reason, e.Detail, e.URL)
}

// NewSSRFGuard creates an empty SSRF guard with no active protections.
// Use the builder methods to configure protections, or use [DefaultSSRFGuard]
// for a preset secure configuration.
func NewSSRFGuard() *SSRFGuard {
	return &SSRFGuard{
		blockedHosts: make(map[string]bool),
		allowedHosts: make(map[string]bool),
		lookupHost:   net.LookupHost,
	}
}

// DefaultSSRFGuard returns a guard preconfigured with:
//   - Cloud metadata endpoint blocking (AWS, Azure, GCP, Alibaba)
//   - Private network blocking (RFC 1918, loopback, link-local, CGNAT, IPv6 ULA,
//     6to4, Teredo, NAT64)
//   - HTTPS enforcement (except localhost)
//
// This is the recommended starting point for extension authors.
func DefaultSSRFGuard() *SSRFGuard {
	return NewSSRFGuard().
		BlockMetadataEndpoints().
		BlockPrivateNetworks().
		RequireHTTPS()
}

// BlockMetadataEndpoints blocks well-known cloud metadata service endpoints:
//   - 169.254.169.254 (AWS, Azure, most cloud providers)
//   - fd00:ec2::254 (AWS EC2 IPv6 metadata)
//   - metadata.google.internal (GCP)
//   - 100.100.100.200 (Alibaba Cloud)
func (g *SSRFGuard) BlockMetadataEndpoints() *SSRFGuard {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blockMetadata = true
	for _, host := range ssrfMetadataHosts {
		g.blockedHosts[strings.ToLower(host)] = true
	}
	return g
}

// BlockPrivateNetworks blocks RFC 1918 private networks, loopback, link-local,
// CGNAT (RFC 6598), and IPv6 transition mechanisms that can embed private IPv4
// addresses (6to4, Teredo, NAT64, IPv4-compatible, IPv4-translated).
func (g *SSRFGuard) BlockPrivateNetworks() *SSRFGuard {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blockPrivate = true
	for _, cidr := range ssrfBlockedCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			g.blockedCIDRs = append(g.blockedCIDRs, ipNet)
		}
	}
	return g
}

// RequireHTTPS requires HTTPS for all URLs except localhost and loopback
// addresses. HTTP to localhost/127.0.0.1/[::1] is always permitted for
// local development.
func (g *SSRFGuard) RequireHTTPS() *SSRFGuard {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requireHTTPS = true
	return g
}

// AllowHost adds hosts to an explicit allowlist. Allowed hosts bypass all
// IP-based and metadata checks. Host names are compared case-insensitively.
//
// Use this sparingly — over-broad allowlists weaken SSRF protection. Prefer
// allowing specific, known-good endpoints rather than wildcards.
func (g *SSRFGuard) AllowHost(hosts ...string) *SSRFGuard {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, h := range hosts {
		g.allowedHosts[strings.ToLower(h)] = true
	}
	return g
}

// OnBlocked registers a callback invoked whenever a URL is blocked. This
// enables security audit logging without coupling the guard to a logging
// framework. The callback receives the machine-readable reason tag and a
// human-readable detail string. It must be safe for concurrent invocation.
func (g *SSRFGuard) OnBlocked(fn func(reason, detail string)) *SSRFGuard {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onBlocked = fn
	return g
}

// Check validates a URL against the guard's SSRF policy.
//
// Validation order:
//  1. Parse the URL and reject non-HTTP(S) schemes.
//  2. If HTTPS is required, reject plain HTTP to non-localhost hosts.
//  3. Skip further checks if the host is explicitly allowed via [AllowHost].
//  4. Skip further checks for localhost/loopback hosts (local development).
//  5. Reject hosts matching the metadata endpoint blocklist.
//  6. For IP-literal hosts, check directly against blocked CIDRs.
//  7. For hostname hosts, resolve DNS (fail-closed on lookup failure) and
//     check all resolved IPs against blocked CIDRs.
//
// For IPv6 addresses, embedded IPv4 (IPv4-compatible, IPv4-mapped,
// IPv4-translated per RFC 2765) is extracted and re-checked against blocked CIDRs.
//
// Returns nil if the URL is allowed, or a [*SSRFError] describing the violation.
func (g *SSRFGuard) Check(rawURL string) error {
	g.mu.RLock()
	err := g.checkCore(rawURL)
	onBlocked := g.onBlocked
	g.mu.RUnlock()

	if err != nil && onBlocked != nil {
		var ssrfErr *SSRFError
		if errors.As(err, &ssrfErr) {
			onBlocked(ssrfErr.Reason, ssrfErr.Detail)
		}
	}

	return err
}

func (g *SSRFGuard) checkCore(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return g.blocked(truncateValue(rawURL, 200), "invalid_url", "URL parsing failed: "+err.Error())
	}

	host := u.Hostname()

	// Step 1: Scheme validation — only http and https permitted.
	switch u.Scheme {
	case "https":
		// Always allowed.
	case "http":
		if g.requireHTTPS && !isLocalhostHost(host) {
			return g.blocked(truncateValue(rawURL, 200), "https_required", "HTTPS is required for non-localhost URLs")
		}
	default:
		return g.blocked(truncateValue(rawURL, 200), "scheme_blocked",
			fmt.Sprintf("scheme %q is not allowed (only http and https are permitted)", u.Scheme))
	}

	lowerHost := strings.ToLower(host)

	// Step 2: Explicit allowlist bypass.
	if g.allowedHosts[lowerHost] {
		return nil
	}

	// Step 3: Localhost/loopback bypass — localhost is the developer's own
	// machine and is exempt from IP-level SSRF blocking to allow local
	// development workflows (e.g. local API servers, proxies, dev tools).
	if isLocalhostHost(host) {
		return nil
	}

	// Step 5: Metadata endpoint check.
	if g.blockedHosts[lowerHost] {
		return g.blocked(truncateValue(rawURL, 200), "blocked_host",
			fmt.Sprintf("host %s is blocked", host))
	}

	// Step 6: IP-based checks.
	if ip := net.ParseIP(host); ip != nil {
		// Direct IP literal — check against blocked ranges.
		return g.checkIPForSSRF(ip, host, rawURL)
	}

	// Step 7: DNS resolution for hostnames (fail-closed).
	addrs, err := g.lookupHost(host)
	if err != nil {
		return g.blocked(truncateValue(rawURL, 200), "dns_failure",
			fmt.Sprintf("DNS resolution failed for %s (fail-closed): %s", host, err.Error()))
	}

	for _, addr := range addrs {
		if g.blockedHosts[strings.ToLower(addr)] {
			return g.blocked(truncateValue(rawURL, 200), "blocked_host",
				fmt.Sprintf("host %s resolved to blocked address %s", host, addr))
		}
		if ip := net.ParseIP(addr); ip != nil {
			if ssrfErr := g.checkIPForSSRF(ip, host, rawURL); ssrfErr != nil {
				return ssrfErr
			}
		}
	}

	return nil
}

// blocked creates an SSRFError.
func (g *SSRFGuard) blocked(urlStr, reason, detail string) *SSRFError {
	return &SSRFError{URL: urlStr, Reason: reason, Detail: detail}
}

// checkIPForSSRF validates an IP address against blocked CIDRs and private
// network categories. It also extracts embedded IPv4 from IPv6 encoding
// variants (IPv4-compatible, IPv4-translated RFC 2765) that Go's net.IP
// methods do not classify.
func (g *SSRFGuard) checkIPForSSRF(ip net.IP, originalHost, rawURL string) error {
	if reason, detail, isBlocked := ssrfCheckIP(ip, originalHost, g.blockedCIDRs, g.blockPrivate); isBlocked {
		return g.blocked(truncateValue(rawURL, 200), reason, detail)
	}
	return nil
}
