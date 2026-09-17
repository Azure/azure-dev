// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/spf13/cobra"
)

const (
	agentVersionOverrideHeader   = "x-agent-version-override"
	agentVersionResolvedHeader   = "x-agent-version-resolved"
	agentVersionResolutionHeader = "x-agent-version-resolution"
	agentVersionFallbackHeader   = "x-agent-version-fallback"
	maxOverrideRecoveryBytes     = 1024 * 1024
	overrideRecoveryTimeout      = 5 * time.Second
)

var errOverrideRecoveryIdentity = errors.New("unverified background work identity received")

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

// verifyVersionOverrideResponse checks the initial POST response before saving
// identities, consuming SSE, or polling a 202. Lifecycle GETs are not new version
// selections and need not repeat these headers. Final 1xx/3xx responses fail;
// 4xx/5xx retain their existing HTTP error handling. A verification failure
// cannot undo an already accepted invocation.
func (a *InvokeAction) verifyVersionOverrideResponse(
	ctx context.Context,
	resp *http.Response,
	rc *remoteContext,
	protocol agent_api.AgentProtocol,
	writer io.Writer,
) error {
	requested := a.flags.versionOverride
	if requested == "" || resp.StatusCode >= http.StatusBadRequest {
		return nil
	}
	var resolved, resolution string
	var err error
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err = fmt.Errorf("unexpected HTTP status %d; expected a successful 2xx response", resp.StatusCode)
	} else {
		resolved, resolution, err = verifyAgentVersionHeaders(requested, resp.Header)
	}
	if err != nil {
		message := fmt.Sprintf("agent version override %q could not be verified: %s", requested, err)
		suggestion := "confirm the candidate version is ready and the endpoint returns version-resolution headers; " +
			"the request may already have executed, so do not automatically retry side-effecting tests"
		if rc != nil && resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices &&
			((protocol == agent_api.AgentProtocolResponses && a.flags.longRunning) ||
				(protocol == agent_api.AgentProtocolInvocations && resp.StatusCode == http.StatusAccepted)) {
			id, recoveryErr := recoverOverrideWorkID(ctx, resp, protocol, a.flags.outputFmt == outputRaw)
			if id != "" {
				label, operation := "Invocation", invocationShow
				if protocol == agent_api.AgentProtocolResponses {
					label, operation = "Response", invocationFollow
				}
				message += fmt.Sprintf("\nUnverified %s ID: %s", label, id)
				suggestion += "\nInspect or cancel the unverified work explicitly (current selection is unchanged):\n  " +
					a.overrideRecoveryCommand(rc, protocol, id, operation) + "\n  " +
					a.overrideRecoveryCommand(rc, protocol, id, invocationCancel)
			}
			if recoveryErr != nil {
				if id == "" {
					suggestion += "\nCould not recover the service-assigned ID: " + recoveryErr.Error()
				} else {
					suggestion += "\nRaw response capture is incomplete: " + recoveryErr.Error()
				}
			}
		}
		verificationErr := exterrors.Compatibility(
			exterrors.CodeAgentVersionVerificationFailed,
			message,
			suggestion,
		)
		if a.flags.outputFmt == outputRaw {
			// Keep raw diagnostics visible, but never poll/follow an unverified version.
			return errors.Join(verificationErr, writeRawResponse(writer, resp))
		}
		return verificationErr
	}
	if a.flags.outputFmt != outputRaw {
		_, err = fmt.Fprintf(writer, "Version override: %s; resolved: %s", requested, resolved)
		if err != nil {
			return err
		}
		if resolution != "" {
			if _, err = fmt.Fprintf(writer, "; resolution: %s", resolution); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintln(writer)
	}
	return err
}

// recoverOverrideWorkID reads only enough of an unverified background response
// to recover its identity, never saving state or following the work. The read is
// bounded even when the streaming client has no overall timeout. Restore captured
// bytes for raw diagnostics; an incomplete capture is reported as a recovery error.
func recoverOverrideWorkID(
	ctx context.Context, resp *http.Response, protocol agent_api.AgentProtocol, captureBody bool,
) (string, error) {
	var headerID string
	if protocol == agent_api.AgentProtocolInvocations {
		if values := resp.Header.Values("x-agent-invocation-id"); len(values) > 0 {
			if len(values) != 1 {
				return "", fmt.Errorf("ambiguous Invocation ID header")
			}
			var err error
			headerID, err = validateOverrideWorkID(values[0])
			if err != nil || !captureBody {
				return headerID, err
			}
		}
	}
	if resp.Body == nil {
		if headerID != "" {
			return headerID, nil
		}
		return "", fmt.Errorf("response body is empty")
	}
	originalBody := resp.Body
	defer originalBody.Close()
	readCtx, cancel := context.WithTimeout(ctx, overrideRecoveryTimeout)
	defer cancel()
	stop := context.AfterFunc(readCtx, func() { _ = originalBody.Close() })
	defer stop()
	var captured bytes.Buffer
	limited := &io.LimitedReader{R: io.TeeReader(originalBody, &captured), N: maxOverrideRecoveryBytes + 1}
	defer func() { resp.Body = io.NopCloser(bytes.NewReader(captured.Bytes())) }()
	var id string
	var err error
	if headerID != "" {
		// The header is authoritative even when the diagnostic body is malformed
		// or stalls. Preserve that identity if bounded raw capture fails.
		_, err = io.Copy(io.Discard, limited)
	} else if protocol == agent_api.AgentProtocolResponses {
		err = readResponsesSSE(readCtx, limited, io.Discard, "", responsesSSEOptions{
			onResponseID: func(value string) error {
				id = value
				return errOverrideRecoveryIdentity
			},
		})
		if errors.Is(err, errOverrideRecoveryIdentity) {
			err = nil
		}
	} else {
		// Reuse the accepted-body parser without letting it close the original
		// stream; this function owns closing and restoring the raw response bytes.
		accepted := *resp
		accepted.Body = io.NopCloser(limited)
		id, err = invocationIDFromResponse(&accepted)
	}
	if readCtx.Err() != nil {
		return headerID, fmt.Errorf("ID recovery interrupted: %w", readCtx.Err())
	}
	if limited.N == 0 {
		return headerID, fmt.Errorf("ID recovery exceeded %d bytes; response capture is incomplete", maxOverrideRecoveryBytes)
	}
	if err != nil {
		// Do not copy arbitrary agent output or malformed event payloads into a
		// recovery command or error suggestion.
		return headerID, fmt.Errorf("could not read the background response identity")
	}
	if headerID != "" {
		return headerID, nil
	}
	return validateOverrideWorkID(id)
}

func validateOverrideWorkID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("the response did not contain a service-assigned ID")
	}
	if len(id) > 1024 || strings.ContainsFunc(id, func(r rune) bool { return !isInvokeVersionChar(r) }) {
		return "", fmt.Errorf("the service-assigned ID is invalid")
	}
	return id, nil
}

// overrideRecoveryCommand binds recovery to the actual project and agent, not
// whichever environment or current operation is selected when the command is run.
func (a *InvokeAction) overrideRecoveryCommand(
	rc *remoteContext, protocol agent_api.AgentProtocol, id string, operation invocationOperation,
) string {
	endpoint := buildInvocationsURL(rc.projectEndpoint, rc.name, rc.apiVersion, "")
	if protocol == agent_api.AgentProtocolResponses {
		endpoint = buildResponsesURL(rc.projectEndpoint, rc.name, rc.apiVersion)
	}
	command := fmt.Sprintf("azd ai agent invocations %s --id %q --agent-endpoint %q", operation, id, endpoint)
	if a.flags.userIdentity != "" {
		command += fmt.Sprintf(" --user-identity %q", a.flags.userIdentity)
	}
	return command
}

// verifyAgentVersionHeaders fails closed for missing or ambiguous evidence.
// The fallback header is optional: the service only emits it for a fallback.
// latest is a floating request value, never evidence of a concrete resolution.
func verifyAgentVersionHeaders(requested string, headers http.Header) (string, string, error) {
	fallbackValues := headers.Values(agentVersionFallbackHeader)
	if len(fallbackValues) > 1 {
		return "", "", fmt.Errorf("multiple %s headers", agentVersionFallbackHeader)
	}
	if len(fallbackValues) == 1 {
		switch strings.ToLower(strings.TrimSpace(fallbackValues[0])) {
		case "true":
			return "", "", fmt.Errorf("the service reported a version fallback")
		case "false":
		default:
			return "", "", fmt.Errorf("invalid %s header", agentVersionFallbackHeader)
		}
	}
	resolved, err := singleAgentVersionHeader(headers, agentVersionResolvedHeader, true)
	if err != nil {
		return "", "", err
	}
	if strings.EqualFold(resolved, "latest") {
		return "", "", fmt.Errorf("the service did not report a concrete resolved version")
	}
	if requested != "latest" && resolved != requested {
		return "", "", fmt.Errorf("resolved version %q does not match requested version %q", resolved, requested)
	}
	resolution, err := singleAgentVersionHeader(headers, agentVersionResolutionHeader, false)
	return resolved, resolution, err
}

func singleAgentVersionHeader(headers http.Header, name string, required bool) (string, error) {
	values := headers.Values(name)
	if len(values) == 0 && !required {
		return "", nil
	}
	if len(values) != 1 {
		return "", fmt.Errorf("expected exactly one %s header", name)
	}
	value := strings.TrimSpace(values[0])
	if value == "" || validateInvokeVersionValue(value) != nil {
		return "", fmt.Errorf("invalid %s header", name)
	}
	return value, nil
}
