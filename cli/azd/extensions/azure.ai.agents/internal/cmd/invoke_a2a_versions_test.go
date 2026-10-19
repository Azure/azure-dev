// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/require"
)

func TestValidateInvokeA2AVersionFlags(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, version, file string
		protocol            agent_api.AgentProtocol
		wantError           string
	}{
		{"default plain", "", "", agent_api.AgentProtocolA2A, ""},
		{"explicit 0.3", "0.3", "", agent_api.AgentProtocolA2A, ""},
		{"explicit 1.0", "1.0", "", agent_api.AgentProtocolA2A, ""},
		{"file 0.3", "0.3", "request.json", agent_api.AgentProtocolA2A, ""},
		{"file 1.0", "1.0", "request.json", agent_api.AgentProtocolA2A, ""},
		{"file missing version", "", "request.json", agent_api.AgentProtocolA2A, "requires an explicit --a2a-version"},
		{"invalid version", "2.0", "", agent_api.AgentProtocolA2A, "supported versions are 0.3 and 1.0"},
		{"no normalization", " 1.0", "", agent_api.AgentProtocolA2A, "invalid --a2a-version"},
		{"responses", "1.0", "", agent_api.AgentProtocolResponses, "only supported with the a2a protocol"},
		{"invocations", "0.3", "", agent_api.AgentProtocolInvocations, "only supported with the a2a protocol"},
		{"other protocol without flag", "", "request.json", agent_api.AgentProtocolResponses, ""},
		{"unresolved explicit", "1.0", "", "", ""},
		{"unresolved file", "", "request.json", "", ""},
		{"unresolved invalid", "latest", "", "", "invalid --a2a-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateInvokeA2AVersionFlags(&invokeFlags{
				a2aVersion: tt.version, inputFile: tt.file,
			}, tt.protocol)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBuildA2ARequestBody_ValidatesVersion(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, explicit, resolved, file, wantError string
	}{
		{"invalid explicit", "2.0", "", "", "invalid --a2a-version"},
		{"invalid resolved", "", "2.0", "", "invalid --a2a-version"},
		{"invalid explicit despite resolved", "2.0", "1.0", "", "invalid --a2a-version"},
		{"file missing explicit", "", "", "missing.json", "requires an explicit --a2a-version"},
		{"file with manifest version", "", "1.0", "missing.json", "requires an explicit --a2a-version"},
		{"file with legacy manifest version", "", "0.3", "missing.json", "requires an explicit --a2a-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{
				flags:              &invokeFlags{message: "hello", inputFile: tt.file, a2aVersion: tt.explicit},
				resolvedA2AVersion: tt.resolved,
			}
			_, _, err := a.buildA2ARequestBody()
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestBuildA2ARequestBody_OpaqueFile(t *testing.T) {
	t.Parallel()

	// A file is opaque: do not parse, validate, normalize, or convert its body.
	content := []byte(" \r\nnot JSON\x00\xff\n")
	path := filepath.Join(t.TempDir(), "opaque.bin")
	require.NoError(t, os.WriteFile(path, content, 0600))
	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{flags: &invokeFlags{inputFile: path, a2aVersion: version}}
			body, _, err := a.buildA2ARequestBody()
			require.NoError(t, err)
			require.Equal(t, content, body)
		})
	}
}

func TestA2AProtocolVersion(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, explicit, resolved, want string }{
		{"default", "", "", "1.0"},
		{"explicit", "0.3", "", "0.3"},
		{"resolved", "", "0.3", "0.3"},
		{"explicit takes precedence", "0.3", "1.0", "0.3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &InvokeAction{flags: &invokeFlags{a2aVersion: tt.explicit}, resolvedA2AVersion: tt.resolved}
			require.Equal(t, tt.want, a.a2aProtocolVersion())
		})
	}
}

func TestExtractA2AText_Versions(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, version, body, want string }{
		{"1.0 message", "1.0", `{"result":{"message":{"role":"ROLE_AGENT","parts":[{"text":"hello"}]}}}`, "hello"},
		{"1.0 task status", "1.0",
			`{"result":{"task":{"status":{"message":{"parts":[{"text":"done"}]}}}}}`, "done"},
		{"1.0 task artifacts", "1.0",
			`{"result":{"task":{"artifacts":[{"parts":[{"text":"one"},{"text":"two"}]}]}}}`, "one\ntwo"},
		{"1.0 task history skips user", "1.0",
			`{"result":{"task":{"history":[{"role":"ROLE_AGENT","parts":[{"text":"answer"}]},` +
				`{"role":"ROLE_USER","parts":[{"text":"question"}]}]}}}`, "answer"},
		{"0.3 history skips both user spellings", "0.3",
			`{"result":{"history":[{"role":"agent","parts":[{"text":"answer"}]},` +
				`{"role":"user","parts":[{"text":"q1"}]},{"role":"ROLE_USER","parts":[{"text":"q2"}]}]}}`, "answer"},
		{"1.0 user only history", "1.0",
			`{"result":{"task":{"history":[{"role":"ROLE_USER","parts":[{"text":"question"}]}]}}}`, ""},
		{"1.0 requires wrapper", "1.0", `{"result":{"parts":[{"text":"legacy"}]}}`, ""},
		{"0.3 does not unwrap 1.0", "0.3", `{"result":{"message":{"parts":[{"text":"new"}]}}}`, ""},
		{"1.0 malformed wrapper", "1.0", `{"result":{"task":"not a task"}}`, ""},
		{"1.0 null result", "1.0", `{"result":null}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extractA2AText([]byte(tt.body), tt.version))
		})
	}
}

func TestHandleA2AResponse_HTTP200JSONRPCError(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					`{"jsonrpc":"2.0","error":{"code":-32601,"message":"method not found"}}`,
				)),
			}
			err := handleA2AResponse(resp, "agent", false, version)
			require.ErrorContains(t, err, "agent error (-32601): method not found")
		})
	}
}
