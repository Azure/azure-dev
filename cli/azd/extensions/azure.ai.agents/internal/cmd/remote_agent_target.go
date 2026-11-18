// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	directAgentEndpointFlag   = "agent-endpoint"
	directProjectEndpointFlag = "project-endpoint"
)

// directAgentTargetFlags selects a deployed agent from explicit cloud identity
// instead of azure.yaml services and azd environment outputs.
type directAgentTargetFlags struct {
	agentEndpoint   string
	projectEndpoint string
}

func addDirectAgentTargetFlags(cmd *cobra.Command, flags *directAgentTargetFlags, nameSelector string) {
	cmd.Flags().StringVar(&flags.agentEndpoint, directAgentEndpointFlag, "",
		"Full protocol endpoint URL of a deployed agent (no azd project required)")
	cmd.Flags().StringVar(&flags.projectEndpoint, directProjectEndpointFlag, "",
		fmt.Sprintf("Foundry project endpoint URL; requires the deployed agent name via %s "+
			"(no azd project required)", nameSelector))
}

// resolveDirectAgentTarget returns the explicitly selected remote agent, or nil when
// neither --agent-endpoint nor --project-endpoint is set. name is the explicit agent
// selector for the command, nameSupplied reports whether the user supplied it (even
// as an empty value), and nameSelector describes it in error messages. All checks
// are local so conflicting inputs fail before authentication.
func resolveDirectAgentTarget(
	cmd *cobra.Command,
	flags *directAgentTargetFlags,
	name string,
	nameSupplied bool,
	nameSelector string,
) (*AgentContext, error) {
	agentEndpointSet := cmd.Flags().Changed(directAgentEndpointFlag)
	projectEndpointSet := cmd.Flags().Changed(directProjectEndpointFlag)
	if !agentEndpointSet && !projectEndpointSet {
		return nil, nil
	}

	if agentEndpointSet && projectEndpointSet {
		return nil, exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--agent-endpoint and --project-endpoint cannot be used together",
			"use --agent-endpoint alone, or --project-endpoint with "+nameSelector,
		)
	}
	if cmd.Flags().Changed("environment") {
		return nil, exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--environment cannot be combined with --agent-endpoint or --project-endpoint",
			"direct targets do not read azd environment values; remove --environment",
		)
	}

	if agentEndpointSet {
		if nameSupplied || name != "" {
			return nil, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf("--agent-endpoint cannot be combined with %s", nameSelector),
				"the agent name is taken from the --agent-endpoint URL; remove "+nameSelector,
			)
		}
		return agentContextFromAgentEndpoint(flags.agentEndpoint)
	}

	projectEndpoint, err := validateDirectProjectEndpoint(flags.projectEndpoint)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			"--project-endpoint requires the deployed agent name",
			fmt.Sprintf("supply the Foundry agent name with %s", nameSelector),
		)
	}
	if err := agent_yaml.ValidateAgentName(name); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			fmt.Sprintf("agent name %q is invalid", name),
			"agent names must start and end with an alphanumeric character, "+
				"may contain hyphens in the middle, and be 1-63 characters long",
		)
	}
	return &AgentContext{ProjectEndpoint: projectEndpoint, Name: name}, nil
}

func agentContextFromAgentEndpoint(raw string) (*AgentContext, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, exterrors.Validation(exterrors.CodeInvalidParameter, "invalid --agent-endpoint URL", agentEndpointHint)
	}
	if strings.EqualFold(u.Scheme, "wss") {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--agent-endpoint does not accept WebSocket (wss) URLs for this command",
			"supply an https agent endpoint, such as the invocations or responses protocol URL",
		)
	}
	query, queryErr := url.ParseQuery(u.RawQuery)
	if u.User != nil || u.Fragment != "" || u.ForceQuery || queryErr != nil ||
		(len(query) > 0 && (len(query) != 1 || len(query["api-version"]) != 1)) {
		return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
			"--agent-endpoint must not contain credentials, a fragment, or unsupported query parameters",
			"use a protocol endpoint URL with only an optional api-version parameter")
	}
	parsed, err := parseAgentEndpoint(raw)
	if err != nil {
		return nil, err
	}
	u, err = url.Parse(parsed.ProjectEndpoint)
	if err != nil {
		return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
			"--agent-endpoint project URL is invalid", agentEndpointHint)
	}
	// Canonicalize percent-encoded project names so requests use one escaping form.
	u.RawPath = ""
	return &AgentContext{ProjectEndpoint: u.String(), Name: parsed.AgentName}, nil
}

func validateDirectProjectEndpoint(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--project-endpoint requires a non-empty URL",
			"supply https://<account>.services.ai.azure.com/api/projects/<project>",
		)
	}
	original, parseErr := url.Parse(strings.TrimSpace(raw))
	if parseErr != nil || original.User != nil || original.RawQuery != "" || original.ForceQuery ||
		original.Fragment != "" {
		return "", exterrors.Validation(exterrors.CodeInvalidParameter,
			"--project-endpoint must not contain credentials, query parameters, or a fragment",
			"supply the Foundry project endpoint URL without those components")
	}
	normalized, pathWarning, err := validateProjectEndpoint(raw)
	if err != nil {
		return "", err
	}
	u, parseErr := url.Parse(normalized)
	project := ""
	if parseErr == nil {
		project = strings.TrimPrefix(u.EscapedPath(), projectEndpointPathPrefix)
	}
	if pathWarning || parseErr != nil || project == "" || strings.Contains(project, "/") ||
		u.RawQuery != "" || u.Fragment != "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--project-endpoint must have the form https://<account>.services.ai.azure.com/api/projects/<project>",
			"to target one agent by URL, use --agent-endpoint instead",
		)
	}
	return normalized, nil
}

// directSessionsListCommand returns a `sessions list` command line that targets the
// same remote agent with the same direct selector the user supplied.
func directSessionsListCommand(flags *directAgentTargetFlags, target *AgentContext) string {
	if flags.agentEndpoint != "" {
		u, err := url.Parse(flags.agentEndpoint)
		if err == nil {
			u.User = nil
			u.RawQuery = ""
			u.ForceQuery = false
			u.Fragment = ""
			u.RawFragment = ""
			return fmt.Sprintf("azd ai agent sessions list --agent-endpoint %q", u.String())
		}
	}
	return fmt.Sprintf("azd ai agent sessions list --agent-name %s --project-endpoint %q",
		target.Name, target.ProjectEndpoint)
}

// validateDirectSessionID requires an explicit session ID in direct mode because
// direct targets never read or write locally stored session selections.
// listCommand is the `sessions list` command suggested for finding a session ID.
func validateDirectSessionID(sessionID, listCommand string) error {
	if strings.TrimSpace(sessionID) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidSessionId,
			"--session-id is required with --agent-endpoint or --project-endpoint",
			fmt.Sprintf("run `%s` to find a session ID", listCommand),
		)
	}
	if strings.ContainsAny(sessionID, "/\\?#") || strings.TrimSpace(sessionID) != sessionID {
		return exterrors.Validation(
			exterrors.CodeInvalidSessionId,
			fmt.Sprintf("session ID %q is invalid", sessionID),
			"supply the session ID exactly as returned by `azd ai agent sessions list`",
		)
	}
	return nil
}

type remoteAgentReader interface {
	GetAgent(context.Context, string, string, bool) (*agent_api.AgentObject, error)
}

// getDirectAgent reads a directly targeted agent and classifies common failures.
func getDirectAgent(ctx context.Context, client remoteAgentReader, target *AgentContext) (*agent_api.AgentObject, error) {
	agent, err := client.GetAgent(ctx, target.Name, DefaultAgentAPIVersion, false)
	if err != nil {
		return nil, classifyDirectAgentError(err, target)
	}
	return agent, nil
}

func classifyDirectAgentError(err error, target *AgentContext) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		switch respErr.StatusCode {
		case http.StatusNotFound:
			return exterrors.Validation(
				exterrors.CodeAgentNotFound,
				fmt.Sprintf("agent %q was not found in project %s", target.Name, target.ProjectEndpoint),
				"check the deployed agent name and project endpoint; "+
					"the name is the Foundry agent name, not an azure.yaml service name",
			)
		case http.StatusUnauthorized, http.StatusForbidden:
			return withServiceSuggestion(exterrors.ServiceFromAzure(err, exterrors.OpGetAgent),
				"confirm your signed-in account can read agents in this Foundry project "+
					"(for example, the Azure AI User role)")
		}
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpGetAgent)
}

// Features named in unsupported-kind errors for directly targeted agents.
const (
	featureSessions     = "sessions"
	featureSessionFiles = "session files"
)

// requireDirectSessionSupport reads a directly targeted agent and rejects kinds
// that never have sessions, before any session API request is sent.
func requireDirectSessionSupport(
	ctx context.Context, client remoteAgentReader, target *AgentContext, feature string,
) error {
	agent, err := getDirectAgent(ctx, client, target)
	if err != nil {
		return err
	}
	return requireSessionSupport(agent, feature)
}

// requireSessionSupport rejects agent kinds that never have sessions or session
// filesystems. Agents without a declared kind are left to the service.
func requireSessionSupport(agent *agent_api.AgentObject, feature string) error {
	definition, _ := agent.Versions.Latest.Definition.(map[string]any)
	kind, _ := definition["kind"].(string)
	switch agent_api.AgentKind(kind) {
	case agent_api.AgentKindPrompt, agent_api.AgentKindWorkflow:
		return exterrors.Compatibility(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("agent %q has kind %q, which does not support %s", agent.Name, kind, feature),
			feature+" are available only for hosted agents",
		)
	}
	return nil
}

// classifySessionFileError maps session file API failures to actionable errors.
// listCommand is the `sessions list` command to suggest; empty uses the project form.
func classifySessionFileError(err error, operation, agentName, sessionID, remotePath, listCommand string) error {
	if listCommand == "" {
		listCommand = "azd ai agent sessions list"
	}
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		switch respErr.StatusCode {
		case http.StatusNotFound:
			target := "session"
			if remotePath != "" {
				target = fmt.Sprintf("session or path %q", remotePath)
			}
			return exterrors.Validation(
				exterrors.CodeSessionNotFound,
				fmt.Sprintf("%s not found for agent %q (session %q)", target, agentName, sessionID),
				fmt.Sprintf("verify the session ID and path; run `%s` to see available sessions", listCommand),
			)
		case http.StatusUnauthorized, http.StatusForbidden:
			return withServiceSuggestion(exterrors.ServiceFromAzure(err, operation),
				"confirm your account can access this agent's sessions; "+
					"for agents with header-based isolation, pass the --user-identity that owns the session")
		}
	}
	return exterrors.ServiceFromAzure(err, operation)
}

func withServiceSuggestion(err error, suggestion string) error {
	if serviceErr, ok := errors.AsType[*azdext.ServiceError](err); ok && serviceErr.Suggestion == "" {
		serviceErr.Suggestion = suggestion
	}
	return err
}
