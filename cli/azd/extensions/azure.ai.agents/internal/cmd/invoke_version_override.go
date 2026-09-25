// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	agentVersionOverrideHeader   = "x-agent-version-override"
	agentVersionResolvedHeader   = "x-agent-version-resolved"
	agentVersionResolutionHeader = "x-agent-version-resolution"
	agentVersionFallbackHeader   = "x-agent-version-fallback"
)

func validateInvokeVersionOverrideFlags(cmd *cobra.Command, flags *invokeFlags) error {
	if flags.versionOverride == "" && !cmd.Flags().Changed("version-override") {
		return nil
	}
	flags.versionOverride = strings.TrimSpace(flags.versionOverride)
	if flags.versionOverride == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentVersion,
			"--version-override requires a non-empty agent version",
			"provide a concrete candidate version, or latest for a floating selection",
		)
	}
	if err := validateInvokeVersionValue(flags.versionOverride); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentVersion,
			fmt.Sprintf("invalid --version-override value %q: %s", flags.versionOverride, err),
			"use a version containing only letters, numbers, dots, underscores, and hyphens",
		)
	}
	for _, flag := range []string{"version", "session-id", "conversation-id"} {
		if cmd.Flags().Changed(flag) {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf("--version-override cannot be combined with --%s", flag),
				"remove the conflicting flag; override tests use a fresh session and conversation",
			)
		}
	}
	for _, flag := range []string{"new-session", "new-conversation"} {
		value, _ := cmd.Flags().GetBool(flag)
		if cmd.Flags().Changed(flag) && !value {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf("--version-override cannot be combined with --%s=false", flag),
				"remove the conflicting flag; override tests never reuse saved session or conversation state",
			)
		}
	}
	return nil
}

func (a *InvokeAction) validateVersionOverrideRoute(protocol agent_api.AgentProtocol, isPrompt bool) error {
	if a.flags.versionOverride == "" {
		return nil
	}
	if !a.flags.local && !isPrompt &&
		(protocol == "" || protocol == agent_api.AgentProtocolResponses || protocol == agent_api.AgentProtocolInvocations) {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeConflictingArguments,
		"--version-override requires a remote hosted agent using responses or invocations",
		"remove --version-override for local, A2A, or prompt agents",
	)
}

func (a *InvokeAction) applyVersionOverride(req *http.Request) {
	if a.flags.versionOverride != "" {
		req.Header.Set(agentVersionOverrideHeader, a.flags.versionOverride)
	}
}

// reportVersionOverrideResponse reports optional routing metadata without consuming the body.
// Only an explicit fallback or concrete version mismatch is a routing failure.
func (a *InvokeAction) reportVersionOverrideResponse(resp *http.Response, writer io.Writer, warnings io.Writer) error {
	requested := a.flags.versionOverride
	if requested == "" || resp.StatusCode >= http.StatusBadRequest {
		return nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unexpected HTTP status %d; expected a successful 2xx response", resp.StatusCode)
	}
	resolved := optionalAgentVersionHeader(resp.Header, agentVersionResolvedHeader)
	resolution := optionalAgentVersionHeader(resp.Header, agentVersionResolutionHeader)
	fallbackValues := resp.Header.Values(agentVersionFallbackHeader)
	incomplete := resolved == "" || len(fallbackValues) > 1
	var reason string
	for _, value := range fallbackValues {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true":
			reason = "the service reported a version fallback"
		case "false":
		default:
			incomplete = true
		}
	}
	if reason == "" && resolved != "" && requested != "latest" && resolved != requested {
		reason = fmt.Sprintf("resolved version %q does not match requested version %q", resolved, requested)
	}
	var routingErr, outputErr error
	if reason != "" {
		routingErr = exterrors.Compatibility(
			exterrors.CodeAgentVersionRoutingFailed,
			fmt.Sprintf("agent version override %q was not honored: %s", requested, reason),
			"confirm the candidate behavior manually; the request may already have executed; no automatic retries",
		)
	}
	if incomplete {
		_, outputErr = fmt.Fprintln(warnings,
			"Warning: agent version information is unavailable or incomplete; confirm the candidate behavior manually.")
	}
	if a.flags.outputFmt != outputRaw {
		if resolved == "" {
			resolved = "not reported"
		}
		summary := fmt.Sprintf("Version override: %s; resolved: %s", requested, resolved)
		if resolution != "" {
			summary += "; resolution: " + resolution
		}
		_, err := fmt.Fprintln(writer, summary)
		outputErr = errors.Join(outputErr, err)
	}
	if outputErr != nil {
		return combineInvokeErrors(routingErr, outputErr)
	}
	return routingErr
}

// optionalAgentVersionHeader ignores missing, ambiguous, or unsafe metadata.
func optionalAgentVersionHeader(headers http.Header, name string) string {
	values := headers.Values(name)
	if len(values) != 1 {
		return ""
	}
	value := strings.TrimSpace(values[0])
	if value == "" || strings.EqualFold(value, "latest") || validateInvokeVersionValue(value) != nil {
		return ""
	}
	return value
}

// combineInvokeErrors preserves all failure messages when WrapError selects one
// structured error for transmission to the azd host. Keep its classification and
// guidance without mutating errors that other callers may still use.
func combineInvokeErrors(routingErr, executionErr error) error {
	if routingErr == nil {
		return executionErr
	}
	if executionErr == nil {
		return routingErr
	}
	combined := errors.Join(routingErr, executionErr)
	if serviceErr, ok := errors.AsType[*azdext.ServiceError](combined); ok {
		result := *serviceErr
		result.Message = combined.Error()
		return &invokeCombinedError{error: &result, cause: combined}
	}
	if localErr, ok := errors.AsType[*azdext.LocalError](combined); ok {
		result := *localErr
		result.Message = combined.Error()
		return &invokeCombinedError{error: &result, cause: combined}
	}
	if toolErr, ok := errors.AsType[*azdext.ToolError](combined); ok {
		result := *toolErr
		result.Message = combined.Error()
		return &invokeCombinedError{error: &result, cause: combined}
	}
	return combined
}

type invokeCombinedError struct {
	error
	cause error
}

// Unwrap exposes the complete structured message first, retaining the original causes.
func (err *invokeCombinedError) Unwrap() []error {
	return []error{err.error, err.cause}
}
