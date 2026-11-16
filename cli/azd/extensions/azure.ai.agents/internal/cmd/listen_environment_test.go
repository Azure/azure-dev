// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/recordproxy"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestPostdownCleansOnlySelectedEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name, selected, want string
	}{
		{name: "explicit non-default", selected: "dev", want: "dev"},
		{name: "explicit default", selected: "prod", want: "prod"},
		{name: "saved default", want: "prod"},
		{name: "unreadable selection", selected: "missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", tt.selected)
			t.Setenv("AZD_CONFIG_DIR", t.TempDir())
			t.Setenv("AGENT_DEFINITION_PATH", "")
			env := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "prod"},
				environments: map[string]*azdext.Environment{
					"dev": {Name: "dev"}, "prod": {Name: "prod"},
				},
				values: map[string]map[string]string{},
			}
			tracked := &postdownEnvironmentServer{testEnvironmentServiceServer: env}
			client := newTestAzdClient(t, tracked, &testWorkflowServiceServer{})
			store, err := newAgentStateStoreForClient(client)
			require.NoError(t, err)
			store.client = nil
			keys := map[string]string{}
			for _, name := range []string{"dev", "prod"} {
				endpoint := "https://" + name + ".services.ai.azure.com/api/projects/" + name +
					"/agents/agent/versions/1"
				keys[name] = buildRemoteAgentKeyFromEndpoint(endpoint)
				saveAgentStateForTest(t, client, keys[name], agentStateRecord{SessionID: name + "-session"})
				env.values[name] = map[string]string{
					"AGENT_AGENT_ENDPOINT":                endpoint,
					"AZURE_SUBSCRIPTION_ID":               "00000000-0000-0000-0000-000000000001",
					"AZURE_TENANT_ID":                     "00000000-0000-0000-0000-000000000002",
					envkey.AgentBotName("agent"):          name + "-custom-bot",
					envkey.AgentBotResourceGroup("agent"): name + "-bots",
					envkey.AgentBotOwned("agent"):         "false",
				}
			}
			proj := &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					"agent": {
						Name: "agent", Host: AiAgentHost,
						AdditionalProperties: mustStruct(t, map[string]any{
							"kind": "hosted", "name": "agent", "image": "example.azurecr.io/agent:latest",
							"protocols": []any{map[string]any{"protocol": "activity", "version": "v1"}},
						}),
					},
				},
			}
			_, err = captureStdout(t, func() error {
				return postdownHandler(t.Context(), client, &azdext.ProjectEventArgs{Project: proj})
			})
			require.NoError(t, err)
			for name, key := range keys {
				record, err := store.get(t.Context(), key)
				require.NoError(t, err)
				if name == tt.want {
					require.Nil(t, record)
				} else {
					require.NotNil(t, record)
					require.Equal(t, name+"-session", record.SessionID)
				}
			}
			if tt.want == "" {
				require.Empty(t, tracked.requests)
			} else {
				var keys []string
				for _, request := range tracked.requests {
					require.Equal(t, tt.want, request.EnvName)
					keys = append(keys, request.Key)
				}
				require.ElementsMatch(t, []string{
					"AGENT_AGENT_ENDPOINT", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID",
					envkey.AgentBotName("agent"), envkey.AgentBotResourceGroup("agent"), envkey.AgentBotOwned("agent"),
				}, keys)
			}
			if tt.selected != "" {
				require.Zero(t, env.getCurrentCalls)
			}
			require.Empty(t, env.setKeys)
		})
	}
}

type postdownEnvironmentServer struct {
	*testEnvironmentServiceServer
	requests []*azdext.GetEnvRequest
}

func (s *postdownEnvironmentServer) GetValue(
	ctx context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	s.requests = append(s.requests, req)
	return s.testEnvironmentServiceServer.GetValue(ctx, req)
}

func TestPredownDeletesOnlySelectedEnvironmentAgent(t *testing.T) {
	for _, tt := range []struct {
		name, selected, enabled, want string
		blocked                       bool
	}{
		{name: "non-reuse selection", selected: "dev", want: "dev"},
		{name: "enabled reuse selection", selected: "prod", enabled: "true", want: "prod"},
		{name: "saved default", enabled: "true", want: "prod"},
		{name: "disabled reuse selection", selected: "prod", blocked: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", tt.selected)
			t.Setenv(synthesis.ACRReuseEnvVar, tt.enabled)
			t.Setenv("AGENT_DEFINITION_PATH", "")
			bin := t.TempDir()
			//nolint:gosec // fake credential for offline HTTP assertions
			token := `{"token":"offline-test-token","expiresOn":"2099-01-01T00:00:00Z"}`
			name, script := "azd", "#!/bin/sh\nprintf '%s\\n' '"+token+"'\n"
			if runtime.GOOS == "windows" {
				name, script = "azd.cmd", "@echo off\r\necho "+token+"\r\n"
			}
			executable := filepath.Join(bin, name)
			require.NoError(t, os.WriteFile(executable, []byte(script), 0o600))
			//nolint:gosec // the offline fixture must execute on Unix
			require.NoError(t, os.Chmod(executable, 0o700))
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			var deletes []string
			previous := recordproxy.Transport
			t.Cleanup(func() { recordproxy.Transport = previous })
			recordproxy.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodDelete, req.Method)
				require.Equal(t, "v1", req.URL.Query().Get("api-version"))
				require.Equal(t, "true", req.URL.Query().Get("force"))
				deletes = append(deletes, req.URL.Host+req.URL.Path)
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader("")), Request: req,
				}, nil
			})
			env := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "prod"},
				environments: map[string]*azdext.Environment{
					"dev": {Name: "dev"}, "prod": {Name: "prod"},
				},
				values: map[string]map[string]string{},
			}
			for _, name := range []string{"dev", "prod"} {
				endpoint := "https://" + name + ".services.ai.azure.com/api/projects/" + name
				env.values[name] = map[string]string{
					"AZURE_SUBSCRIPTION_ID":        "subscription",
					"AZURE_RESOURCE_GROUP":         name,
					"FOUNDRY_PROJECT_ENDPOINT":     endpoint,
					"AGENT_AGENT_NAME":             name + "-deployed",
					"AGENT_AGENT_PROJECT_ENDPOINT": endpoint,
				}
			}
			env.values["prod"]["AZD_FOUNDRY_ACR_MODE"] = "reuse-connect"
			env.values["prod"]["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] =
				"/subscriptions/subscription/resourceGroups/prod/providers/Microsoft.ContainerRegistry/registries/shared"
			address := newInvokeRemoteContextTestAzdServer(
				t, &helpersProjectServer{}, env, &rbacMessageAccountServer{},
			)
			client, err := azdext.NewAzdClient(azdext.WithAddress(address))
			require.NoError(t, err)
			t.Cleanup(func() { client.Close() })
			proj := &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					"agent": {
						Name: "agent", Host: AiAgentHost,
						AdditionalProperties: mustStruct(t, map[string]any{
							"kind": "prompt", "name": "manifest-name", "model": "gpt-4.1", "instructions": "Help.",
						}),
					},
				},
			}
			_, err = captureStdout(t, func() error {
				return predownHandler(t.Context(), client, &azdext.ProjectEventArgs{Project: proj})
			})
			if tt.blocked {
				requireACRFeatureDisabled(t, err)
				require.Empty(t, deletes)
			} else {
				require.NoError(t, err)
				require.Equal(t, []string{
					tt.want + ".services.ai.azure.com/api/projects/" + tt.want + "/agents/" + tt.want + "-deployed",
				}, deletes)
			}
			if tt.selected != "" {
				require.Zero(t, env.getCurrentCalls)
			}
			require.Empty(t, env.setKeys)
		})
	}
}
