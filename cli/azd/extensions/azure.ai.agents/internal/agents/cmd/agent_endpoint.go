// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"azureaiagent/internal/agents/exterrors"
	"azureaiagent/internal/agents/pkg/agents/agent_api"
	"azureaiagent/internal/agents/pkg/agents/agent_yaml"
)

// agentEndpointHostSuffix is the required Foundry host suffix for endpoint URLs.
const agentEndpointHostSuffix = ".services.ai.azure.com"

// agentEndpointHint is the suggestion appended to most --agent-endpoint validation errors.
// `azd ai agent show` persistently prints the agent endpoint URL, so it's the right
// thing to point users at any time after a deploy.
const agentEndpointHint = "run `azd ai agent show` to see the agent endpoint URL"

// agentEndpointPathRegex matches the full Foundry agent-endpoint path. Captures:
//
//	[1] project name (URL-escaped),
//	[2] agent name (URL-escaped),
//	[3] protocol tail ("invocations" or "openai/responses").
var agentEndpointPathRegex = regexp.MustCompile(
	`^/api/projects/([^/]+)/agents/([^/]+)/endpoint/protocols/(invocations|openai/responses)/?$`,
)

// parsedAgentEndpoint describes a deployed agent invocation endpoint.
type parsedAgentEndpoint struct {
	// ProjectEndpoint is the Foundry project root: https://<acct>.services.ai.azure.com/api/projects/<proj>.
	ProjectEndpoint string
	AgentName       string
	Protocol        agent_api.AgentProtocol
	// APIVersion is the api-version query parameter from the URL, or empty if absent.
	APIVersion string
}

// parseAgentEndpoint parses the full agent invocation URL printed by `azd ai agent show`.
//
// Accepted shapes:
//
//	https://<acct>.services.ai.azure.com/api/projects/<proj>/agents/<name>/endpoint/protocols/invocations[?api-version=…]
//	https://<acct>.services.ai.azure.com/api/projects/<proj>/agents/<name>/endpoint/protocols/openai/responses[?api-version=…]
//
// The host must be a `*.services.ai.azure.com` Foundry host. The path must include the
// protocol-specific suffix; the protocol is derived from the URL.
func parseAgentEndpoint(rawURL string) (*parsedAgentEndpoint, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-endpoint requires a non-empty URL",
			agentEndpointHint,
		)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid --agent-endpoint URL: %v", err),
			agentEndpointHint,
		)
	}

	if !strings.EqualFold(u.Scheme, "https") {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-endpoint must use https",
			agentEndpointHint,
		)
	}

	host := strings.ToLower(u.Hostname())
	if host == "" || !strings.HasSuffix(host, agentEndpointHostSuffix) {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--agent-endpoint host %q is not a Foundry host (*%s)", u.Hostname(), agentEndpointHostSuffix),
			agentEndpointHint,
		)
	}

	// Reject explicit ports — Foundry endpoints always use the default HTTPS port,
	// and silently dropping a non-default port would route requests to a different origin.
	if u.Port() != "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--agent-endpoint host %q must not include a port", u.Host),
			agentEndpointHint+" (no explicit port)",
		)
	}

	// Match the full path against the canonical Foundry agent-endpoint shape and pull
	// the project name, agent name, and protocol tail out in one pass.
	matches := agentEndpointPathRegex.FindStringSubmatch(u.EscapedPath())
	if matches == nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-endpoint path must match /api/projects/<project>/agents/<name>/endpoint/protocols/<protocol>",
			agentEndpointHint,
		)
	}
	projectSegment, agentSegment, protocolTail := matches[1], matches[2], matches[3]

	projectName, err := url.PathUnescape(projectSegment)
	if err != nil || projectName == "" || strings.ContainsAny(projectName, "/\\") {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-endpoint project segment is invalid",
			agentEndpointHint,
		)
	}

	agentName, err := url.PathUnescape(agentSegment)
	if err != nil || agent_yaml.ValidateAgentName(agentName) != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			fmt.Sprintf("--agent-endpoint agent name %q is invalid", agentSegment),
			"agent names must start and end with an alphanumeric character, "+
				"may contain hyphens in the middle, and be 1-63 characters long",
		)
	}

	var protocol agent_api.AgentProtocol
	switch protocolTail {
	case "invocations":
		protocol = agent_api.AgentProtocolInvocations
	case "openai/responses":
		protocol = agent_api.AgentProtocolResponses
	}

	// Reject an explicit but empty api-version query parameter; the default fallback would
	// otherwise silently invoke a different version than the user pasted.
	apiVersion := ""
	query := u.Query()
	if values, present := query["api-version"]; present {
		if len(values) == 0 || values[0] == "" {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--agent-endpoint api-version query parameter is empty",
				"include a non-empty api-version value or omit the parameter to use the default",
			)
		}
		apiVersion = values[0]
	}

	projectEndpoint := fmt.Sprintf("https://%s/api/projects/%s", host, projectSegment)

	return &parsedAgentEndpoint{
		ProjectEndpoint: projectEndpoint,
		AgentName:       agentName,
		Protocol:        protocol,
		APIVersion:      apiVersion,
	}, nil
}

// buildResponsesURL builds the Foundry "openai/responses" protocol URL for an agent.
// apiVersion is URL-encoded so unusual characters cannot break out of the query value.
func buildResponsesURL(projectEndpoint, agentName, apiVersion string) string {
	return fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/openai/responses?api-version=%s",
		projectEndpoint, agentName, url.QueryEscape(apiVersion),
	)
}

// buildInvocationsURL builds the Foundry "invocations" protocol URL for an agent.
// When sid is non-empty, an agent_session_id query parameter is appended (URL-encoded).
func buildInvocationsURL(projectEndpoint, agentName, apiVersion, sid string) string {
	invURL := fmt.Sprintf(
		"%s/agents/%s/endpoint/protocols/invocations?api-version=%s",
		projectEndpoint, agentName, url.QueryEscape(apiVersion),
	)
	if sid != "" {
		invURL += "&agent_session_id=" + url.QueryEscape(sid)
	}
	return invURL
}

// (isValidAgentNameSegment was removed — agent name validation now delegates
// to agent_yaml.ValidateAgentName so --agent-endpoint enforces the same
// deployable-name format as the rest of the extension.)
