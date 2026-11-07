// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const publicIPv4Endpoint = "https://api.ipify.org"

type publicIPProvider interface {
	Discover(context.Context) (string, error)
}

type httpPublicIPProvider struct {
	client *http.Client
}

func newPublicIPProvider() publicIPProvider {
	return &httpPublicIPProvider{
		client: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: rejectPublicIPRedirect,
		},
	}
}

func rejectPublicIPRedirect(*http.Request, []*http.Request) error {
	return errors.New("public IPv4 discovery redirects are not allowed")
}

func (p *httpPublicIPProvider) Discover(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, publicIPv4Endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("creating public IPv4 discovery request: %w", err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("discovering the public IPv4 address: %w", nonDisclosureError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("public IPv4 discovery returned HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 65))
	if err != nil {
		return "", fmt.Errorf("reading the public IPv4 discovery response: %w", err)
	}
	if len(content) > 64 {
		return "", validationError(
			"public_ip_response_too_large",
			"the public IPv4 discovery response exceeded the allowed size",
			"set POSTGRES_BOOTSTRAP_CLIENT_IP to the exact public IPv4 address and retry",
		)
	}
	return validatePublicIPv4(strings.TrimSpace(string(content)))
}

func validateBootstrapClientIPEnvironment(lookup func(string) (string, bool)) error {
	value, present := lookup(postgresBootstrapClientIPEnv)
	if !present {
		return nil
	}
	if value == "" {
		return validationError(
			"empty_postgres_bootstrap_client_ip",
			fmt.Sprintf("%s is explicitly empty", postgresBootstrapClientIPEnv),
			"remove the variable to use automatic discovery or set one exact public IPv4 address",
		)
	}
	_, err := validatePublicIPv4(value)
	return err
}

func discoverBootstrapIPv4(
	ctx context.Context,
	lookup func(string) (string, bool),
	provider publicIPProvider,
) (string, error) {
	if value, present := lookup(postgresBootstrapClientIPEnv); present {
		if value == "" {
			return "", validationError(
				"empty_postgres_bootstrap_client_ip",
				fmt.Sprintf("%s is explicitly empty", postgresBootstrapClientIPEnv),
				"remove the variable to use automatic discovery or set one exact public IPv4 address",
			)
		}
		return validatePublicIPv4(value)
	}
	if provider == nil {
		return "", validationError(
			"public_ip_discovery_unavailable",
			"public IPv4 discovery is unavailable",
			"set POSTGRES_BOOTSTRAP_CLIENT_IP to the exact public IPv4 address and retry",
		)
	}
	value, err := provider.Discover(ctx)
	if err != nil {
		return "", err
	}
	return validatePublicIPv4(value)
}

func validatePublicIPv4(raw string) (string, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || !ip.Is4() {
		return "", validationError(
			"invalid_postgres_bootstrap_client_ip",
			"the PostgreSQL bootstrap client address is not one IPv4 address",
			"set POSTGRES_BOOTSTRAP_CLIENT_IP to the exact public IPv4 address",
		)
	}
	for _, prefix := range specialUseIPv4Prefixes {
		if prefix.Contains(ip) {
			return "", validationError(
				"non_public_postgres_bootstrap_client_ip",
				"the PostgreSQL bootstrap client address is not a public IPv4 address",
				"set POSTGRES_BOOTSTRAP_CLIENT_IP to the exact public egress IPv4 address",
			)
		}
	}
	return ip.String(), nil
}

var specialUseIPv4Prefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}
