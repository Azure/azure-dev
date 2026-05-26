// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// rewrittenAuthEndpoint is the canonical placeholder URL used as the
// AZD_AUTH_ENDPOINT after rewriting unix:/npipe: schemes. RemoteCredential
// formats the request URL as "<endpoint>/token?api-version=..." so this
// placeholder produces a syntactically valid URL whose host/path are
// irrelevant because the transport dials a fixed socket/pipe.
const rewrittenAuthEndpoint = "http://azd-auth"

// buildExternalAuthConfiguration constructs the auth.ExternalAuthConfiguration
// from the raw AZD_AUTH_* env values. It dispatches on the scheme of the
// endpoint URL:
//
//   - "" or "https": existing loopback HTTPS behavior. AZD_AUTH_CERT is
//     required for "https".
//   - "unix": POSIX-only Unix domain socket transport. Cert MUST NOT be set.
//     Key is optional but still forwarded for defense in depth.
//   - "npipe": Windows-only named pipe transport. Cert MUST NOT be set. Key
//     is optional but still forwarded for defense in depth.
//
// Any other scheme yields an error that lists the supported schemes.
func buildExternalAuthConfiguration(endpoint, key, cert string) (auth.ExternalAuthConfiguration, error) {
	// Parse the endpoint up front so we can dispatch on its scheme. An empty
	// endpoint string parses successfully with an empty scheme, which is the
	// historical "no external auth configured" / "implicit http for tests"
	// case.
	endpointUrl, err := url.Parse(endpoint)
	if err != nil {
		return auth.ExternalAuthConfiguration{},
			fmt.Errorf("invalid AZD_AUTH_ENDPOINT value '%s': %w", endpoint, err)
	}

	switch endpointUrl.Scheme {
	case "", "http", "https":
		return buildHTTPSExternalAuth(endpoint, key, cert, endpointUrl.Scheme)
	case "unix":
		return buildLocalIPCExternalAuth(endpoint, key, cert, newSocketTransport)
	case "npipe":
		return buildLocalIPCExternalAuth(endpoint, key, cert, newPipeTransport)
	default:
		return auth.ExternalAuthConfiguration{}, fmt.Errorf(
			"invalid AZD_AUTH_ENDPOINT value '%s': unsupported scheme %q "+
				"(supported schemes: https, unix, npipe)",
			endpoint, endpointUrl.Scheme)
	}
}

// buildHTTPSExternalAuth implements the historical HTTPS / no-scheme path.
// When a cert is provided, the scheme MUST be "https".
func buildHTTPSExternalAuth(endpoint, key, cert, scheme string) (auth.ExternalAuthConfiguration, error) {
	client := &http.Client{}
	if len(cert) > 0 {
		transport, err := httputil.TlsEnabledTransport(cert)
		if err != nil {
			return auth.ExternalAuthConfiguration{},
				fmt.Errorf("parsing AZD_AUTH_CERT: %w", err)
		}
		client.Transport = transport

		if scheme != "https" {
			return auth.ExternalAuthConfiguration{},
				fmt.Errorf(
					"invalid AZD_AUTH_ENDPOINT value '%s': scheme must be 'https' when certificate is provided",
					endpoint)
		}
	}
	return auth.ExternalAuthConfiguration{
		Endpoint:    endpoint,
		Transporter: client,
		Key:         key,
	}, nil
}

// buildLocalIPCExternalAuth implements the unix: / npipe: paths. Both share
// the same shape: cert is forbidden, key is optional, the transport is built
// by the platform-specific factory, and the endpoint is rewritten to a
// canonical placeholder so RemoteCredential can format request URLs.
func buildLocalIPCExternalAuth(
	endpoint, key, cert string,
	newTransport func(string) (http.RoundTripper, string, error),
) (auth.ExternalAuthConfiguration, error) {
	if len(cert) > 0 {
		return auth.ExternalAuthConfiguration{}, fmt.Errorf(
			"AZD_AUTH_CERT must not be set when AZD_AUTH_ENDPOINT uses a local IPC scheme " +
				"(unix:, npipe:); the OS enforces caller identity")
	}
	transport, rewritten, err := newTransport(endpoint)
	if err != nil {
		return auth.ExternalAuthConfiguration{}, err
	}
	return auth.ExternalAuthConfiguration{
		Endpoint:    rewritten,
		Transporter: &http.Client{Transport: transport},
		Key:         key,
	}, nil
}
