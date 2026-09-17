// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeVersionOverrideFlagValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "empty", args: []string{"--version-override="}, want: "requires a non-empty"},
		{name: "whitespace", args: []string{"--version-override", " \t"}, want: "requires a non-empty"},
		{name: "slash", args: []string{"--version-override=bad/version"}, want: "unsupported characters"},
		{name: "injection", args: []string{"--version-override", "3\r\nx-header: value"}, want: "unsupported characters"},
		{name: "long", args: []string{"--version-override=" + strings.Repeat("a", 129)}, want: "at most 128"},
		{name: "version", args: []string{"--version-override=3", "--version=4"}, want: "combined with --version"},
		{name: "session", args: []string{"--version-override=3", "--session-id=old"}, want: "combined with --session-id"},
		{name: "empty session", args: []string{"--version-override=3", "--session-id="}, want: "combined with --session-id"},
		{
			name: "conversation", args: []string{"--version-override=3", "--conversation-id=old"},
			want: "combined with --conversation-id",
		},
		{
			name: "reuse session", args: []string{"--version-override=3", "--new-session=false"},
			want: "combined with --new-session=false",
		},
		{
			name: "reuse conversation", args: []string{"--version-override=3", "--new-conversation=false"},
			want: "combined with --new-conversation=false",
		},
		{
			name: "local", args: []string{"--version-override=3", "--local"},
			want: "requires a remote hosted agent",
		},
		{
			name: "a2a", args: []string{"--version-override=3", "--protocol=a2a"},
			want: "requires a remote hosted agent",
		},
		{
			name: "endpoint a2a", args: []string{"--version-override=3", "--agent-endpoint",
				"https://acct.services.ai.azure.com/api/projects/proj/agents/agent/endpoint/protocols/a2a"},
			want: "requires a remote hosted agent",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newInvokeCommand(&azdext.ExtensionContext{NoPrompt: true})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append(tt.args, "hello"))
			require.ErrorContains(t, cmd.Execute(), tt.want)
		})
	}
}

func TestInvokeVersionOverrideRegistration(t *testing.T) {
	cmd := newInvokeCommand(&azdext.ExtensionContext{})
	flag := cmd.Flags().Lookup("version-override")
	require.NotNil(t, flag)
	assert.Empty(t, flag.DefValue)
	assert.Empty(t, flag.Shorthand)
	assert.Contains(t, flag.Usage, "fail on fallback")
	assert.Contains(t, cmd.Long, "without changing its traffic split")
	assert.Contains(t, cmd.Long, "does not undo work already executed")
	assert.Contains(t, cmd.Flags().Lookup("version").Usage, "session backed by that version")
}

func TestVerifyAgentVersionHeaders(t *testing.T) {
	for _, tt := range []struct {
		name       string
		requested  string
		resolved   []string
		fallback   []string
		resolution []string
		wantErr    string
	}{
		{name: "exact", requested: "3", resolved: []string{"3"}, resolution: []string{"flightoverride"}},
		{name: "optional headers absent", requested: "3", resolved: []string{"3"}},
		{name: "false fallback", requested: "3", resolved: []string{"3"}, fallback: []string{"false"}},
		{name: "trim", requested: "3", resolved: []string{" 3 "}, fallback: []string{" FALSE "}},
		{name: "latest", requested: "latest", resolved: []string{"9"}},
		{name: "missing", requested: "3", wantErr: "expected exactly one"},
		{name: "empty", requested: "3", resolved: []string{""}, wantErr: "invalid"},
		{name: "invalid", requested: "3", resolved: []string{"3,4"}, wantErr: "invalid"},
		{name: "duplicate", requested: "3", resolved: []string{"3", "3"}, wantErr: "exactly one"},
		{name: "mismatch", requested: "3", resolved: []string{"4"}, wantErr: "does not match"},
		{name: "unresolved latest", requested: "latest", resolved: []string{"latest"}, wantErr: "concrete"},
		{
			name: "fallback", requested: "3", resolved: []string{"3"}, fallback: []string{"true"},
			wantErr: "reported a version fallback",
		},
		{
			name: "latest fallback", requested: "latest", resolved: []string{"3"}, fallback: []string{"TRUE"},
			wantErr: "reported a version fallback",
		},
		{
			name: "ambiguous fallback", requested: "3", resolved: []string{"3"}, fallback: []string{"false", "true"},
			wantErr: "multiple",
		},
		{
			name: "invalid fallback", requested: "3", resolved: []string{"3"}, fallback: []string{"0"},
			wantErr: "invalid",
		},
		{
			name: "ambiguous resolution", requested: "3", resolved: []string{"3"},
			resolution: []string{"flightoverride", "default"}, wantErr: "exactly one",
		},
		{
			name: "invalid resolution", requested: "3", resolved: []string{"3"},
			resolution: []string{"bad\x1b[31m"}, wantErr: "invalid",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			for name, values := range map[string][]string{
				agentVersionResolvedHeader: tt.resolved, agentVersionFallbackHeader: tt.fallback,
				agentVersionResolutionHeader: tt.resolution,
			} {
				for _, value := range values {
					headers.Add(name, value)
				}
			}
			resolved, resolution, err := verifyAgentVersionHeaders(tt.requested, headers)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.resolved[0]), resolved)
			if len(tt.resolution) > 0 {
				assert.Equal(t, tt.resolution[0], resolution)
			}
		})
	}
}

func TestInvokeVersionOverrideHeaderAndResponseIsolation(t *testing.T) {
	for _, version := range []string{"", "3", "latest"} {
		t.Run("request/"+version, func(t *testing.T) {
			action := &InvokeAction{flags: &invokeFlags{versionOverride: version}}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test", nil)
			require.NoError(t, err)
			action.applyVersionOverride(req)
			assert.Equal(t, version, req.Header.Get(agentVersionOverrideHeader))
			assert.Equal(t, version != "", req.Header.Values(agentVersionOverrideHeader) != nil)
		})
	}
	for _, tt := range []struct {
		name    string
		version string
		status  int
	}{
		{name: "ordinary invocation", status: 200},
		{name: "bad request", version: "3", status: 400},
		{name: "session conflict", version: "3", status: 409},
		{name: "server failure", version: "3", status: 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := &trackingReadCloser{Reader: strings.NewReader("original response")}
			resp := &http.Response{StatusCode: tt.status, Header: make(http.Header), Body: body}
			action := &InvokeAction{flags: &invokeFlags{versionOverride: tt.version}}
			var output bytes.Buffer
			require.NoError(t, action.verifyVersionOverrideResponse(resp, &output))
			assert.Empty(t, output.String())
			assert.False(t, body.closed)
			remaining, err := io.ReadAll(body)
			require.NoError(t, err)
			assert.Equal(t, "original response", string(remaining))
		})
	}
}

func TestInvokeVersionOverrideNoVersionSession(t *testing.T) {
	action := &InvokeAction{flags: &invokeFlags{versionOverride: "4"}}
	rc := &remoteContext{version: "3", agentKey: "ordinary"}
	session, err := action.resolveRemoteSessionID(t.Context(), rc)
	require.NoError(t, err)
	assert.Empty(t, session, "never call CreateSession(version_ref) or reuse the previous session")
	assert.NoError(t, action.validateVersionOverrideRoute(agent_api.AgentProtocolResponses, false))
	assert.NoError(t, action.validateVersionOverrideRoute(agent_api.AgentProtocolInvocations, false))
}
