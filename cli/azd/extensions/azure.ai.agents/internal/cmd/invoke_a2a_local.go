// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/spf13/cobra"
)

const defaultLocalA2APath = "/a2a"

var localA2AUnsupportedFlags = []string{
	"session-id", "new-session", "conversation-id", "new-conversation", "version", "user-identity", "call-id",
}

// captureLocalA2AExplicitFlags preserves explicit empty/false flags for the
// second validation pass, after automatic protocol selection.
func captureLocalA2AExplicitFlags(cmd *cobra.Command, flags *invokeFlags) {
	flags.a2aPathExplicit = cmd.Flags().Changed("a2a-path")
	flags.localA2AExplicitFlags = nil
	for _, name := range localA2AUnsupportedFlags {
		if cmd.Flags().Changed(name) {
			flags.localA2AExplicitFlags = append(flags.localA2AExplicitFlags, name)
		}
	}
}

// validateLocalA2AOptions runs before local project lookup and again after
// protocol resolution. An empty protocol defers only the route-specific checks.
func validateLocalA2AOptions(flags *invokeFlags, protocol agent_api.AgentProtocol) error {
	pathSet := flags.a2aPathExplicit || (flags.a2aPath != "" && flags.a2aPath != defaultLocalA2APath)
	if pathSet {
		if !flags.local || (protocol != "" && protocol != agent_api.AgentProtocolA2A) {
			return exterrors.Validation(exterrors.CodeInvalidParameter,
				"--a2a-path is only supported with --local and the a2a protocol",
				"use --local --protocol a2a, or omit --a2a-path")
		}
		if err := validateLocalA2APath(flags.a2aPath); err != nil {
			return err
		}
	}
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

// validateLocalA2APath accepts only an absolute URL path. Reject encodings that
// intermediaries could decode into routing syntax, including double escapes.
// Safe escapes (such as %20) remain byte-for-byte unchanged on the request.
func validateLocalA2APath(path string) error {
	invalid := func() error {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"invalid --a2a-path: expected an absolute path without authority, query, fragment, or ambiguous escapes",
			"use a path such as /a2a or /rpc; do not provide a URL, backslashes, or dot segments")
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "\\?#") || strings.ContainsFunc(path, func(r rune) bool { return r <= ' ' || r == 127 }) {
		return invalid()
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" {
		return invalid()
	}
	for i := 0; i < len(path); i++ {
		if path[i] != '%' {
			continue
		}
		if len(path)-i < 3 {
			return invalid()
		}
		decoded, err := url.PathUnescape(path[i : i+3])
		if err != nil || strings.ContainsAny(decoded, "/\\.?#%") || decoded[0] < ' ' || decoded[0] == 127 {
			return invalid()
		}
		i += 2
	}
	for segment := range strings.SplitSeq(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return invalid()
		}
	}
	return nil
}

// a2aLocal posts once to the configured localhost path. It deliberately does not
// resolve credentials or Foundry sessions, probe alternate routes, or redirect.
func (a *InvokeAction) a2aLocal(ctx context.Context, version string) error {
	if err := validateLocalInvokePort(a.flags.port); err != nil {
		return err
	}
	if err := validateLocalA2AOptions(a.flags, agent_api.AgentProtocolA2A); err != nil {
		return err
	}
	path := a.flags.a2aPath
	if path == "" {
		path = defaultLocalA2APath
	}
	if err := validateLocalA2APath(path); err != nil {
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
	reqURL := fmt.Sprintf("http://localhost:%d%s", a.flags.port, path)
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
		return fmt.Errorf("POST %s failed (is the agent running? Start it with: azd ai agent run): %w", reqURL, err)
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
		return err
	}
	if !raw {
		printInvokeTiming(w, time.Since(start))
	}
	return nil
}
