// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakePublicIPProvider struct {
	value string
	err   error
	calls int
}

func (p *fakePublicIPProvider) Discover(context.Context) (string, error) {
	p.calls++
	return p.value, p.err
}

func TestDiscoverBootstrapIPv4UsesExplicitOverride(t *testing.T) {
	provider := &fakePublicIPProvider{value: "8.8.4.4"}
	ip, err := discoverBootstrapIPv4(
		t.Context(),
		func(name string) (string, bool) {
			require.Equal(t, postgresBootstrapClientIPEnv, name)
			return "8.8.8.8", true
		},
		provider,
	)
	require.NoError(t, err)
	require.Equal(t, "8.8.8.8", ip)
	require.Zero(t, provider.calls)
}

func TestDiscoverBootstrapIPv4RejectsExplicitEmptyWithoutDiscovery(t *testing.T) {
	provider := &fakePublicIPProvider{value: "8.8.8.8"}
	_, err := discoverBootstrapIPv4(
		t.Context(),
		func(string) (string, bool) { return "", true },
		provider,
	)
	require.ErrorContains(t, err, "explicitly empty")
	require.Zero(t, provider.calls)
}

func TestValidatePublicIPv4RejectsBroadOrNonPublicInputs(t *testing.T) {
	for _, value := range []string{
		"",
		"0.0.0.0",
		"10.0.0.1",
		"100.64.0.1",
		"127.0.0.1",
		"169.254.1.1",
		"172.31.255.255",
		"192.0.0.9",
		"192.0.2.1",
		"192.31.196.1",
		"192.52.193.1",
		"192.88.99.1",
		"192.168.1.1",
		"192.175.48.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"240.0.0.1",
		"255.255.255.255",
		"2001:4860:4860::8888",
		"8.8.8.8-8.8.8.9",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := validatePublicIPv4(value)
			require.Error(t, err)
		})
	}
	ip, err := validatePublicIPv4("8.8.8.8")
	require.NoError(t, err)
	require.Equal(t, "8.8.8.8", ip)
}

func TestDiscoverBootstrapIPv4UsesInjectedProvider(t *testing.T) {
	provider := &fakePublicIPProvider{value: "8.8.4.4"}
	ip, err := discoverBootstrapIPv4(
		t.Context(),
		func(string) (string, bool) { return "", false },
		provider,
	)
	require.NoError(t, err)
	require.Equal(t, "8.8.4.4", ip)
	require.Equal(t, 1, provider.calls)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPublicIPv4DiscoveryRejectsRedirectWithoutFollowingIt(t *testing.T) {
	calls := 0
	provider := &httpPublicIPProvider{
		client: &http.Client{
			CheckRedirect: rejectPublicIPRedirect,
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "api.ipify.org", request.URL.Host)
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"https://example.com/credential?sig=secret"},
					},
					Body:    io.NopCloser(strings.NewReader("")),
					Request: request,
				}, nil
			}),
		},
	}

	_, err := provider.Discover(t.Context())
	require.ErrorContains(t, err, "redirects are not allowed")
	require.NotContains(t, err.Error(), "sig=secret")
	require.Equal(t, 1, calls)
}
