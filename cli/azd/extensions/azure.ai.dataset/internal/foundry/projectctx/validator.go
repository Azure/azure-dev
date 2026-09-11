// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"fmt"
	"net/url"
	"strings"

	"azureaidataset/internal/messages"
)

// foundryHostSuffixes is the authoritative list of accepted Foundry host suffixes.
//
// Public Azure only. The suffix is not the whole story: the client asks for a
// token scoped to https://ai.azure.com/.default, which is a public-cloud
// audience, so accepting a sovereign host here would move the failure from a
// clear message to a 401 nobody can act on.
var foundryHostSuffixes = []string{
	".services.ai.azure.com",
}

// sovereignHostSuffixes are Foundry hosts in clouds this extension does not
// reach yet.
//
// Listed so the refusal can say which problem it is. Without them a Government
// or China endpoint is reported as not being a Foundry host at all, which
// sends a reader to check a URL that is perfectly correct -- the endpoint is
// fine, the support is missing.
var sovereignHostSuffixes = map[string]string{
	".services.ai.azure.us": "Azure Government",
	".services.ai.azure.cn": "Microsoft Azure in China",
}

// sovereignCloudOf names the cloud a host belongs to, when it is one this
// extension cannot reach.
func sovereignCloudOf(hostname string) (string, bool) {
	h := strings.ToLower(hostname)
	for suffix, cloud := range sovereignHostSuffixes {
		if strings.HasSuffix(h, suffix) {
			return cloud, true
		}
	}
	return "", false
}

// projectEndpointPathPrefix is the expected path prefix for Foundry project endpoints.
const projectEndpointPathPrefix = "/api/projects/"

// isFoundryHost reports whether the hostname ends with a recognized Foundry suffix.
func isFoundryHost(hostname string) bool {
	h := strings.ToLower(hostname)
	for _, suffix := range foundryHostSuffixes {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// Validate validates and normalizes a Foundry project endpoint URL.
//
// The URL must be an absolute https:// URL whose host ends with a recognized
// Foundry suffix. Whitespace is trimmed, trailing slashes are stripped, and
// the result is returned in normalized form.
//
// The second return value is true when the path does not look like
// /api/projects/<proj> — callers may use this as a non-fatal warning.
func Validate(raw string) (normalized string, pathWarning bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, messages.EndpointEmpty()
	}

	u, parseErr := url.Parse(raw)
	if parseErr != nil {
		return "", false, messages.EndpointUnparseable(parseErr)
	}

	if !strings.EqualFold(u.Scheme, "https") {
		return "", false, messages.EndpointNotHTTPS()
	}

	host := u.Hostname()
	if host == "" || !isFoundryHost(host) {
		if cloud, ok := sovereignCloudOf(host); ok {
			return "", false, messages.EndpointInAnotherCloud(host, cloud)
		}
		return "", false, messages.EndpointNotFoundryHost(host, foundryHostSuffixes[0])
	}

	if u.Port() != "" {
		return "", false, messages.EndpointHasPort(u.Host)
	}

	// Normalize: lowercase host, strip trailing slash.
	path := strings.TrimRight(u.EscapedPath(), "/")
	normalized = fmt.Sprintf("https://%s%s", strings.ToLower(host), path)

	// Warn when the path does not look like /api/projects/<proj>.
	if !strings.HasPrefix(path, projectEndpointPathPrefix) ||
		strings.TrimPrefix(path, projectEndpointPathPrefix) == "" {
		pathWarning = true
	}

	return normalized, pathWarning, nil
}

// NoEndpointError returns the structured dependency error used when no project
// endpoint could be resolved from any source.
func NoEndpointError() error {
	return messages.NoEndpoint()
}
