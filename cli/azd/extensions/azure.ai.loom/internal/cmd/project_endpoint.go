// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"azure.ai.loom/internal/exterrors"
)

func validateProjectEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint must not be empty",
			"provide a Foundry project endpoint URL",
		)
	}

	endpoint, err := url.Parse(raw)
	if err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"invalid project endpoint URL",
			"provide a valid https:// Foundry project endpoint URL",
		)
	}
	if !strings.EqualFold(endpoint.Scheme, "https") {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint must use https",
			"provide an https:// URL",
		)
	}
	host := strings.ToLower(endpoint.Hostname())
	if host == "" || !strings.HasSuffix(host, ".services.ai.azure.com") {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("project endpoint host %q is not a recognized Foundry host", host),
			"provide a project endpoint whose host ends with .services.ai.azure.com",
		)
	}
	if endpoint.Port() != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint must not include credentials, a port, query parameters, or a fragment",
			"provide the base Foundry project endpoint",
		)
	}
	for segment := range strings.SplitSeq(endpoint.Path, "/") {
		if segment == "." || segment == ".." {
			return "", exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"project endpoint path must not contain dot segments",
				"provide a standard Foundry project endpoint path",
			)
		}
	}

	path := strings.TrimRight(endpoint.EscapedPath(), "/")
	return fmt.Sprintf("https://%s%s", host, path), nil
}

func noProjectEndpointError() error {
	return exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"provide --project-endpoint, configure `azd ai project set <endpoint>`, "+
			"or set FOUNDRY_PROJECT_ENDPOINT",
	)
}
