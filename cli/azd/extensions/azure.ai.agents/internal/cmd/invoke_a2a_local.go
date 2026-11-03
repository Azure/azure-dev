// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/spf13/cobra"
)

var localA2AUnsupportedFlags = []string{
	"session-id", "new-session", "conversation-id", "new-conversation", "version", "user-identity", "call-id",
}

// captureLocalA2AExplicitFlags preserves empty/false options for validation after protocol selection.
func captureLocalA2AExplicitFlags(cmd *cobra.Command, flags *invokeFlags) {
	flags.localA2AExplicitFlags = nil
	for _, name := range localA2AUnsupportedFlags {
		if cmd.Flags().Changed(name) {
			flags.localA2AExplicitFlags = append(flags.localA2AExplicitFlags, name)
		}
	}
}

// validateLocalA2AOptions defers route-specific checks until the protocol is known.
func validateLocalA2AOptions(flags *invokeFlags, protocol agent_api.AgentProtocol) error {
	if !flags.local || protocol != agent_api.AgentProtocolA2A {
		return nil
	}
	checks := []struct {
		name string
		set  bool
	}{
		{"session-id", flags.session != ""}, {"new-session", flags.newSession},
		{"conversation-id", flags.conversation != ""}, {"new-conversation", flags.newConversation},
		{"version", flags.version != ""}, {"user-identity", flags.userIdentity != ""}, {"call-id", flags.callID != ""},
		{"client-header", len(flags.clientHeaders) != 0},
	}
	for _, check := range checks {
		if check.set || slices.Contains(flags.localA2AExplicitFlags, check.name) {
			return exterrors.Validation(exterrors.CodeInvalidParameter,
				fmt.Sprintf("--%s is not supported with local a2a invocation", check.name),
				"omit this flag; local A2A sends public JSON-RPC requests without Foundry headers or session state")
		}
	}
	return nil
}

func validateLocalInvokePort(port int) error {
	if port < 1 || port > 65535 {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"--port must be between 1 and 65535 for local invocation", "provide a valid local server port")
	}
	return nil
}

// a2aLocal invokes the local A2A server without Foundry credentials or session state.
func (a *InvokeAction) a2aLocal(ctx context.Context, version string) error {
	if err := validateLocalInvokePort(a.flags.port); err != nil {
		return err
	}
	if err := validateLocalA2AOptions(a.flags, agent_api.AgentProtocolA2A); err != nil {
		return err
	}
	if len(a.clientHeaders) != 0 {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"--client-header is not supported with the a2a protocol", "omit --client-header")
	}
	body, bodyLabel, err := a.buildA2ARequestBody(version)
	if err != nil {
		return err
	}
	w, progress := a.a2aWriter, a.a2aErrorWriter
	if w == nil {
		w = os.Stdout
	}
	if progress == nil {
		progress = os.Stderr
	}
	raw := a.flags.outputFmt == outputRaw
	reqURL := fmt.Sprintf("http://localhost:%d/a2a", a.flags.port)
	if !raw {
		fmt.Fprintf(w, "Target:   %s (local, a2a protocol)\nInput:    %s\n\n", reqURL, bodyLabel)
	}
	// Use a single context budget across refused-connect retries and body reads.
	// http.Client.Timeout alone would restart the budget on each retry.
	if timeout := a.httpTimeout(); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	newReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create local A2A request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(a2aVersionHeader, version)
		if raw {
			req.Header.Set("Accept-Encoding", "identity")
		}
		return req, nil
	}
	transport := a.a2aTransport
	if transport == nil {
		// No environment proxy: a local invocation must not leave the local host.
		localTransport := &http.Transport{Proxy: nil}
		defer localTransport.CloseIdleConnections()
		transport = localTransport
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	start := time.Now()
	resp, err := doLocalRequestWithRetryWriter(ctx, client, newReq, progress)
	if err != nil {
		if isConnectionRefused(err) {
			return fmt.Errorf("POST %s failed (is the agent running? Start it with: azd ai agent run): %w", reqURL, err)
		}
		return fmt.Errorf("POST %s failed: %w", reqURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if raw {
			if err := handleA2AResponseTo(w, resp, "local", true, version); err != nil {
				return err
			}
		}
		return fmt.Errorf("POST %s returned HTTP %d: redirects are not followed for local A2A", reqURL, resp.StatusCode)
	}
	if err := handleA2AResponseTo(w, resp, "local", raw, version); err != nil {
		if !raw && resp.StatusCode >= 400 {
			_ = printNextIfTerminal(w, nextstep.ResolveAfterInvoke(nil, nextstep.InvokeLocal, "", &nextstep.InvokeFailure{}))
		}
		return err
	}
	if !raw {
		printInvokeTiming(w, time.Since(start))
		_ = printNextIfTerminal(w, nextstep.ResolveAfterInvoke(nil, nextstep.InvokeLocal, "", nil))
	}
	return nil
}
