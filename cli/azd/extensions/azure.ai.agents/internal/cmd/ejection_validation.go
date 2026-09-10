// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/synthesis"
)

func validateEjectedConnectionCredentials(parameters map[string]any) error {
	credentials, ok := parameters["connectionCredentials"]
	if !ok || credentials == nil {
		return nil
	}
	if err := synthesis.ValidateEjectionCredentials(credentials); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("cannot eject concrete connection credentials: %s", err),
			"replace concrete credential values with ${VAR} environment references "+
				"or supported Foundry server-side references, then retry",
		)
	}
	return nil
}

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
