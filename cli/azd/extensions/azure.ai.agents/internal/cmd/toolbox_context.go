// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"log"
	"strings"

	"azureaiagent/internal/pkg/azure"
)

func trimEndpoint(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// newToolboxClient builds a FoundryToolboxClient bound to the resolved endpoint.
func newToolboxClient(endpoint string) (*azure.FoundryToolboxClient, error) {
	cred, err := newAgentCredential()
	if err != nil {
		return nil, err
	}
	return azure.NewFoundryToolboxClient(endpoint, cred), nil
}

// newProjectsClientFromEndpoint builds a FoundryProjectsClient bound to the
// account+project parsed out of the toolbox endpoint URL.
func newProjectsClientFromEndpoint(endpoint string) (*azure.FoundryProjectsClient, error) {
	account, project, err := parseAccountProjectFromEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	cred, err := newAgentCredential()
	if err != nil {
		return nil, err
	}
	return azure.NewFoundryProjectsClient(account, project, cred)
}

// parseAccountProjectFromEndpoint extracts account + project names from an endpoint
// formatted as `https://<account>.services.ai.azure.com/api/projects/<project>` (with
// optional trailing path).
func parseAccountProjectFromEndpoint(endpoint string) (account, project string, err error) {
	trimmed := trimEndpoint(endpoint)
	const marker = ".services.ai.azure.com/api/projects/"
	idx := strings.Index(trimmed, marker)
	if idx < 0 {
		return "", "", fmt.Errorf(
			"endpoint %q does not match the expected pattern <account>.services.ai.azure.com/api/projects/<project>",
			endpoint,
		)
	}
	hostPart := trimmed[:idx]
	if schemeIdx := strings.Index(hostPart, "://"); schemeIdx >= 0 {
		hostPart = hostPart[schemeIdx+3:]
	}
	rest := trimmed[idx+len(marker):]
	projectName := rest
	if slash := strings.Index(rest, "/"); slash >= 0 {
		projectName = rest[:slash]
	}
	if hostPart == "" || projectName == "" {
		return "", "", fmt.Errorf("endpoint %q is missing the account or project segment", endpoint)
	}
	return hostPart, projectName, nil
}

// logResolvedEndpoint records the resolved endpoint and source to --debug.
func logResolvedEndpoint(verb string, r *resolvedEndpoint) {
	if r == nil {
		return
	}
	log.Printf("%s: resolved project endpoint %s (source=%s)", verb, r.Endpoint, r.Source)
}
