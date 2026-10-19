// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectA2ATargetVersion(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, native, explicit string
		responses              bool
		want, wantError        string
	}{
		{name: "unknown target", want: "1.0"},
		{name: "translation", responses: true, want: "1.0"},
		{name: "translation explicit legacy", responses: true, explicit: "0.3", want: "0.3"},
		{name: "native", native: "1.0.0", want: "1.0"},
		{name: "native minor selector", native: "1.0", want: "1.0"},
		{name: "native patch compatible", native: "1.0.7", want: "1.0"},
		{name: "native explicit", native: "1.0.0", explicit: "1.0", want: "1.0"},
		{name: "native legacy mismatch", native: "1.0.0", explicit: "0.3", wantError: "1.0-only agent"},
		{name: "native plus Responses legacy", native: "1.0.0", responses: true, explicit: "0.3", want: "0.3"},
		{name: "legacy native only", native: "0.3.0", wantError: "0.3 passthrough is not supported"},
		{name: "legacy native explicit new", native: "0.3", explicit: "1.0", wantError: "0.3 passthrough"},
		{name: "legacy plus Responses", native: "0.3.0", responses: true, want: "1.0"},
		{name: "legacy plus Responses explicit", native: "0.3", responses: true, explicit: "0.3", want: "0.3"},
		{name: "future minor", native: "1.1.0", wantError: "unsupported native A2A version"},
		{name: "future plus Responses", native: "1.1.0", responses: true, want: "1.0"},
		{name: "old plus Responses", native: "0.2", responses: true, explicit: "1.0", want: "1.0"},
		{name: "future major", native: "2.0.0", wantError: "unsupported native A2A version"},
		{name: "malformed", native: "latest", wantError: "unsupported native A2A version"},
		{name: "negative patch", native: "1.0.-1", wantError: "unsupported native A2A version"},
		{name: "extra component", native: "1.0.0.0", wantError: "unsupported native A2A version"},
		{name: "invalid explicit", explicit: "2.0", wantError: "invalid --a2a-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var protocols []agent_yaml.ProtocolVersionRecord
			if tt.native != "" {
				protocols = append(protocols, agent_yaml.ProtocolVersionRecord{Protocol: "a2a", Version: tt.native})
			}
			if tt.responses {
				protocols = append(protocols, agent_yaml.ProtocolVersionRecord{Protocol: "responses", Version: "2.0.0"})
			}
			got, err := selectA2ATargetVersion(protocols, tt.explicit)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestSelectA2ATargetVersionConflictingDeclarations(t *testing.T) {
	t.Parallel()
	_, err := selectA2ATargetVersion([]agent_yaml.ProtocolVersionRecord{
		{Protocol: "a2a", Version: "0.3.0"}, {Protocol: "a2a", Version: "1.0.0"},
	}, "")
	require.ErrorContains(t, err, "conflicting native A2A versions")
}

func TestNativeA2ARemoteTarget(t *testing.T) {
	for _, tt := range []struct {
		name, native, explicit, wantError string
		fileBacked                        bool
		missingRef                        bool
		responsesOnly                     bool
	}{
		{name: "inline native", native: "1.0.0"},
		{name: "missing local reference still invokes", missingRef: true},
		{name: "public A2A is not native", responsesOnly: true, explicit: "0.3"},
		{name: "file-backed native", native: "1.0.0", fileBacked: true},
		{name: "native 0.3 rejected before send", native: "0.3.0", wantError: "0.3 passthrough"},
		{name: "explicit conflict before send", native: "1.0.0", explicit: "0.3", wantError: "1.0-only agent"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			properties := map[string]any{
				"kind": "hosted", "name": "deployed-agent",
				"protocols":     []any{map[string]any{"protocol": "a2a", "version": tt.native}},
				"agentEndpoint": map[string]any{"protocols": []any{"a2a"}},
			}
			if tt.fileBacked {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "native.yaml"), []byte(
					"kind: hosted\nname: deployed-agent\nprotocols:\n  - protocol: a2a\n    version: 1.0.0\n"), 0600))
				properties = map[string]any{"$ref": "./native.yaml"}
			}
			if tt.missingRef {
				properties = map[string]any{"$ref": "./missing.yaml"}
			}
			if tt.responsesOnly {
				properties["protocols"] = []any{map[string]any{"protocol": "responses", "version": "2.0.0"}}
			}
			wireVersion := "1.0"
			if tt.explicit != "" {
				wireVersion = tt.explicit
			}
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: dir,
				Services: map[string]*azdext.ServiceConfig{"target": {
					Name: "target", Host: AiAgentHost, AdditionalProperties: mustStruct(t, properties),
				}},
			}}
			address := newInvokeRemoteContextTestAzdServer(t, project, &testEnvironmentServiceServer{})
			t.Setenv("AZD_SERVER", address)
			client, err := azdext.NewAzdClient()
			require.NoError(t, err)
			defer client.Close()
			var sends int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends++
				assert.Equal(t, wireVersion, r.Header.Get("A2A-Version"))
				assert.Equal(t, "/agents/deployed-agent/endpoint/protocols/a2a", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if wireVersion == "0.3" {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":`+
						`{"artifacts":[{"parts":[{"text":"native-mock-result"}]}]}}`)
				} else {
					_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{"task":`+
						`{"artifacts":[{"parts":[{"text":"native-mock-result"}]}]}}}`)
				}
			}))
			defer server.Close()
			action := &InvokeAction{
				flags:      &invokeFlags{protocol: "a2a", message: "hello", a2aVersion: tt.explicit},
				credential: responseTestCredential{}, noPrompt: true,
				resolvedRemoteContext: &remoteContext{
					name: "deployed-agent", serviceName: "target", projectEndpoint: server.URL,
					apiVersion: "v1", azdClient: client,
				},
			}
			output, err := captureStdout(t, func() error { return action.a2aRemote(t.Context()) })
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				assert.Zero(t, sends)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, sends)
				assert.Equal(t, wireVersion, action.a2aProtocolVersion())
				assert.Contains(t, output, "native-mock-result")
			}
		})
	}
}
