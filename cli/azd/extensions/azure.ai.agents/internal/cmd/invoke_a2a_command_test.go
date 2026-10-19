// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeA2AVersionCommandValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"missing file version", []string{"-p", "a2a", "-f", "missing.json"}, "requires an explicit --a2a-version"},
		{"invalid version", []string{"-p", "a2a", "--a2a-version", "2.0", "hello"}, "supported versions are 0.3 and 1.0"},
		{"empty version", []string{"-p", "a2a", "--a2a-version=", "hello"}, "invalid --a2a-version"},
		{"responses", []string{"-p", "responses", "--a2a-version", "1.0", "hello"}, "only supported with the a2a protocol"},
		{"invocations", []string{"-p", "invocations", "--a2a-version", "0.3", "hello"},
			"only supported with the a2a protocol"},
		{"endpoint file", []string{"--agent-endpoint",
			"https://acct.services.ai.azure.com/api/projects/project/agents/agent/endpoint/protocols/a2a",
			"-f", "missing.json"}, "requires an explicit --a2a-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := newInvokeCommand(&azdext.ExtensionContext{NoPrompt: true})
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(tt.args)
			require.ErrorContains(t, command.Execute(), tt.want)
		})
	}
}

func TestA2ARemoteSelectedDialect(t *testing.T) {
	for _, version := range []string{"", "0.3", "1.0"} {
		t.Run("version="+version, func(t *testing.T) {
			wireVersion, method, role := "1.0", "SendMessage", "ROLE_USER"
			if version == "0.3" {
				wireVersion, method, role = "0.3", "message/send", "user"
			}
			var sends int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends++
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/agents/agent/endpoint/protocols/a2a", r.URL.Path)
				assert.Equal(t, wireVersion, r.Header.Get("A2A-Version"))
				assert.NotEmpty(t, r.Header.Get("Authorization"))
				var request struct {
					Method string `json:"method"`
					Params struct {
						Message struct {
							Role string `json:"role"`
						} `json:"message"`
					} `json:"params"`
				}
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				assert.Equal(t, method, request.Method)
				assert.Equal(t, role, request.Params.Message.Role)
				w.Header().Set("Content-Type", "application/json")
				if wireVersion == "1.0" {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"message":{"parts":[{"text":"dialect-ok"}]}}}`)
				} else {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"parts":[{"kind":"text","text":"dialect-ok"}]}}`)
				}
			}))
			defer server.Close()
			action := &InvokeAction{
				flags:      &invokeFlags{message: "hello", a2aVersion: version},
				credential: responseTestCredential{},
				resolvedRemoteContext: &remoteContext{
					name: "agent", projectEndpoint: server.URL, apiVersion: "v1",
				},
			}
			output, err := captureStdout(t, func() error { return action.a2aRemote(t.Context()) })
			require.NoError(t, err)
			assert.Equal(t, 1, sends)
			assert.Contains(t, output, "[agent] dialect-ok")
		})
	}
}
