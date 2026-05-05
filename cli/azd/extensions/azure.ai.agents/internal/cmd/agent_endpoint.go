// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
)

// agentEndpointHostSuffix is the required Foundry host suffix for endpoint URLs.
const agentEndpointHostSuffix = ".services.ai.azure.com"

// parsedAgentEndpoint describes a deployed agent invocation endpoint.
type parsedAgentEndpoint struct {
	// ProjectEndpoint is the Foundry project root: https://<acct>.services.ai.azure.com/api/projects/<proj>.
	ProjectEndpoint string
	AgentName       string
	Protocol        agent_api.AgentProtocol
	// APIVersion is the api-version query parameter from the URL, or empty if absent.
	APIVersion string
}

// parseAgentEndpoint parses the full agent invocation URL printed by `azd up` / `azd deploy`.
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
			"pass the agent endpoint printed by `azd up` or `azd deploy`",
		)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid --agent-endpoint URL: %v", err),
			"pass the agent endpoint printed by `azd up` or `azd deploy`",
		)
	}

	if !strings.EqualFold(u.Scheme, "https") {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-endpoint must use https",
			"pass the agent endpoint printed by `azd up` or `azd deploy`",
		)
	}

	host := strings.ToLower(u.Hostname())
	if host == "" || !strings.HasSuffix(host, agentEndpointHostSuffix) {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--agent-endpoint host %q is not a Foundry host (*%s)", u.Hostname(), agentEndpointHostSuffix),
			"pass the agent endpoint printed by `azd up` or `azd deploy`",
		)
	}

	// Reject explicit ports — Foundry endpoints always use the default HTTPS port,
	// and silently dropping a non-default port would route requests to a different origin.
	if u.Port() != "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--agent-endpoint host %q must not include a port", u.Host),
			"pass the agent endpoint printed by `azd up` or `azd deploy` (no explicit port)",
		)
	}

	path := strings.TrimSuffix(u.EscapedPath(), "/")
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")

	// Required prefix: api/projects/<proj>/agents/<name>/endpoint/protocols/<tail…>
	// Minimum 8 segments (invocations); responses has 9 (openai/responses tail).
	if len(segments) < 8 ||
		segments[0] != "api" ||
		segments[1] != "projects" ||
		segments[2] == "" ||
		segments[3] != "agents" ||
		segments[4] == "" ||
		segments[5] != "endpoint" ||
		segments[6] != "protocols" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-endpoint path must match /api/projects/<project>/agents/<name>/endpoint/protocols/<protocol>",
			"pass the agent endpoint printed by `azd up` or `azd deploy`",
		)
	}

	projectName, err := url.PathUnescape(segments[2])
	if err != nil || projectName == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-endpoint project segment is invalid",
			"pass the agent endpoint printed by `azd up` or `azd deploy`",
		)
	}

	agentName, err := url.PathUnescape(segments[4])
	if err != nil || !isValidAgentNameSegment(agentName) {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			fmt.Sprintf("--agent-endpoint agent name %q is invalid", segments[4]),
			"agent names may only contain letters, digits, '-' and '_'",
		)
	}

	tail := segments[7:]
	var protocol agent_api.AgentProtocol
	switch {
	case len(tail) == 1 && tail[0] == "invocations":
		protocol = agent_api.AgentProtocolInvocations
	case len(tail) == 2 && tail[0] == "openai" && tail[1] == "responses":
		protocol = agent_api.AgentProtocolResponses
	default:
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--agent-endpoint protocol path %q is not recognized", strings.Join(tail, "/")),
			"expected '/endpoint/protocols/invocations' or '/endpoint/protocols/openai/responses'",
		)
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

	projectEndpoint := fmt.Sprintf("https://%s/api/projects/%s", host, segments[2])

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

// isValidAgentNameSegment reports whether s is safe to use as a URL path segment
// without escaping. Allowed characters: ASCII letters, digits, '-' and '_'.
func isValidAgentNameSegment(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// printEphemeralSessionHint prints a continuation hint after an ephemeral invoke
// when the server assigned a new session ID. It tells the user how to keep the
// next call on the same session.
func printEphemeralSessionHint(currentSid string, resp *http.Response) {
	if currentSid != "" || resp == nil {
		return
	}
	newSid := resp.Header.Get("x-agent-session-id")
	if newSid == "" {
		return
	}
	fmt.Printf("\nServer assigned session: %s\n", newSid)
	fmt.Printf("To continue this session on the next invoke, pass: --session-id %s\n", newSid)
}

// printEphemeralConversationHint prints a continuation hint after an ephemeral
// invoke when the CLI auto-created a Foundry conversation. It tells the user
// how to keep multi-turn memory on the next invoke, since ephemeral mode does
// not persist conversation state anywhere.
func printEphemeralConversationHint(currentConvID, createdConvID string) {
	if currentConvID != "" || createdConvID == "" {
		return
	}
	fmt.Printf("To continue this conversation on the next invoke, pass: --conversation-id %s\n", createdConvID)
}
