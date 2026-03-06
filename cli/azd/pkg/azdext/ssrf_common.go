// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"net"
)

// ssrfMetadataHosts lists well-known cloud metadata service hostnames/IPs.
var ssrfMetadataHosts = []string{
	"169.254.169.254",
	"fd00:ec2::254",
	"metadata.google.internal",
	"100.100.100.200",
}

// ssrfBlockedCIDRs lists CIDR blocks for private, loopback, link-local, and
// IPv6 transition mechanism networks.
var ssrfBlockedCIDRs = []string{
	"0.0.0.0/8",      // "this" network (reaches loopback on Linux/macOS)
	"10.0.0.0/8",     // RFC 1918 private
	"172.16.0.0/12",  // RFC 1918 private
	"192.168.0.0/16", // RFC 1918 private
	"127.0.0.0/8",    // loopback
	"100.64.0.0/10",  // RFC 6598 shared/CGNAT
	"169.254.0.0/16", // IPv4 link-local
	"::1/128",        // IPv6 loopback
	"::/128",         // IPv6 unspecified
	"fc00::/7",       // IPv6 unique local (RFC 4193)
	"fe80::/10",      // IPv6 link-local
	"2002::/16",      // 6to4 relay (deprecated RFC 7526)
	"2001::/32",      // Teredo tunneling (deprecated)
	"64:ff9b::/96",   // NAT64 well-known prefix (RFC 6052)
	"64:ff9b:1::/48", // NAT64 local-use prefix (RFC 8215)
}

func ssrfCheckIP(
	ip net.IP,
	originalHost string,
	blockedCIDRs []*net.IPNet,
	blockPrivate bool,
) (string, string, bool) {
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return "blocked_ip", fmt.Sprintf("IP %s matches blocked CIDR %s (host: %s)", ip, cidr, originalHost), true
		}
	}

	if !blockPrivate {
		return "", "", false
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return "private_network", fmt.Sprintf("IP %s is private/loopback/link-local (host: %s)", ip, originalHost), true
	}

	if len(ip) != net.IPv6len || ip.To4() != nil {
		return "", "", false
	}

	if v4 := extractIPv4Compatible(ip); v4 != nil {
		for _, cidr := range blockedCIDRs {
			if cidr.Contains(v4) {
				return "blocked_ip", fmt.Sprintf(
					"IP %s (IPv4-compatible %s, CIDR %s) for host %s",
					ip, v4, cidr, originalHost,
				), true
			}
		}
		if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
			return "private_network", fmt.Sprintf(
				"IP %s (IPv4-compatible %s, private/loopback) for host %s",
				ip, v4, originalHost,
			), true
		}
	}

	if v4 := extractIPv4Translated(ip); v4 != nil {
		for _, cidr := range blockedCIDRs {
			if cidr.Contains(v4) {
				return "blocked_ip", fmt.Sprintf(
					"IP %s (IPv4-translated %s, CIDR %s) for host %s",
					ip, v4, cidr, originalHost,
				), true
			}
		}
		if v4.IsLoopback() || v4.IsPrivate() || v4.IsLinkLocalUnicast() || v4.IsUnspecified() {
			return "private_network", fmt.Sprintf(
				"IP %s (IPv4-translated %s, private/loopback) for host %s",
				ip, v4, originalHost,
			), true
		}
	}

	return "", "", false
}

// extractIPv4Compatible extracts the embedded IPv4 from an IPv4-compatible
// IPv6 address (::x.x.x.x — first 12 bytes zero, last 4 non-zero).
func extractIPv4Compatible(ip net.IP) net.IP {
	for i := 0; i < 12; i++ {
		if ip[i] != 0 {
			return nil
		}
	}
	if ip[12] == 0 && ip[13] == 0 && ip[14] == 0 && ip[15] == 0 {
		return nil
	}
	return net.IPv4(ip[12], ip[13], ip[14], ip[15])
}

// extractIPv4Translated extracts the embedded IPv4 from an IPv4-translated
// IPv6 address (::ffff:0:x.x.x.x — RFC 2765 §4.2.1).
func extractIPv4Translated(ip net.IP) net.IP {
	for i := 0; i < 8; i++ {
		if ip[i] != 0 {
			return nil
		}
	}
	if ip[8] != 0xFF || ip[9] != 0xFF || ip[10] != 0x00 || ip[11] != 0x00 {
		return nil
	}
	if ip[12] == 0 && ip[13] == 0 && ip[14] == 0 && ip[15] == 0 {
		return nil
	}
	return net.IPv4(ip[12], ip[13], ip[14], ip[15])
}
