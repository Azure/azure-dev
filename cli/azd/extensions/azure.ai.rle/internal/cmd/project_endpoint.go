// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	foundryProjectEndpointEnvVar = "FOUNDRY_PROJECT_ENDPOINT"
)

func resolveFoundryProjectEndpoint() (string, error) {
	if endpoint := strings.TrimSpace(os.Getenv(foundryProjectEndpointEnvVar)); endpoint != "" {
		return normalizeFoundryProjectEndpoint(endpoint)
	}

	return "", nil
}

func normalizeFoundryProjectEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", invalidProjectEndpointError(fmt.Sprintf("invalid Foundry project endpoint: %v", err))
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", invalidProjectEndpointError("Foundry project endpoint must use https")
	}
	if u.Hostname() == "" || !strings.HasSuffix(strings.ToLower(u.Hostname()), ".services.ai.azure.com") {
		return "", invalidProjectEndpointError("Foundry project endpoint host must end with .services.ai.azure.com")
	}
	if _, err := projectNameFromFoundryEndpoint(u.String()); err != nil {
		return "", err
	}

	u.Scheme = "https"
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func projectNameFromFoundryEndpoint(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", invalidProjectEndpointError(fmt.Sprintf("invalid Foundry project endpoint: %v", err))
	}

	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "projects" || parts[2] == "" {
		return "", invalidProjectEndpointError(
			"Foundry project endpoint path must be /api/projects/<project>",
		)
	}

	projectName, err := url.PathUnescape(parts[2])
	if err != nil {
		return "", invalidProjectEndpointError(fmt.Sprintf("invalid Foundry project name: %v", err))
	}
	if strings.TrimSpace(projectName) == "" {
		return "", invalidProjectEndpointError("Foundry project name must not be empty")
	}
	return projectName, nil
}

func projectRouteSegment(state rleState) (string, error) {
	return projectNameFromFoundryEndpoint(state.ProjectEndpoint)
}

func invalidProjectEndpointError(message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     "rle_invalid_project_endpoint",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.",
			foundryProjectEndpointEnvVar,
		),
	}
}
