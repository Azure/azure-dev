// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
)

func normalizeContainerRegistryEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	parsed, err := url.Parse(raw)
	endpointPart := raw
	if index := strings.IndexAny(endpointPart, "?#"); index >= 0 {
		endpointPart = endpointPart[:index]
	}
	if err != nil || parsed == nil ||
		(parsed.Scheme != "" && parsed.Host == "") ||
		(parsed.User == nil && strings.Contains(endpointPart, "@")) {
		return "", invalidContainerRegistryEndpointError()
	}

	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	normalized := parsed.String()
	if normalized == "" {
		return "", invalidContainerRegistryEndpointError()
	}
	return normalized, nil
}

func invalidContainerRegistryEndpointError() error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		"AZURE_CONTAINER_REGISTRY_ENDPOINT is not a valid registry endpoint",
		"set AZURE_CONTAINER_REGISTRY_ENDPOINT to a registry login "+
			"server without credentials, query parameters, or fragments, "+
			"then retry",
	)
}
