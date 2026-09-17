// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInvokeLatencyLocalRoute(t *testing.T) {
	for _, protocol := range []string{"responses", "invocations"} {
		for _, explicitProtocol := range []bool{false, true} {
			for _, flag := range []string{"", "--debug-latency=false", "--debug-latency=true", "--debug-latency"} {
				name := protocol + "/explicit-protocol=" + strconv.FormatBool(explicitProtocol) + "/" + flag
				t.Run(name, func(t *testing.T) {
					project := &helpersProjectServer{project: &azdext.ProjectConfig{
						Path: t.TempDir(),
						Services: map[string]*azdext.ServiceConfig{"agent": {
							Name: "agent", Host: AiAgentHost,
							AdditionalProperties: mustStruct(t, map[string]any{
								"kind": "hosted", "name": "agent",
								"protocols": []any{map[string]any{"protocol": protocol, "version": "2.0.0"}},
							}),
						}},
					}}
					address := newInvokeRemoteContextTestAzdServer(t, project, &testEnvironmentServiceServer{
						current: &azdext.Environment{Name: "test"},
					})
					t.Setenv("AZD_SERVER", address)
					var posts atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodPost {
							http.NotFound(w, r)
							return
						}
						posts.Add(1)
						assert.Equal(t, "/"+protocol, r.URL.Path)
						assert.Empty(t, r.Header.Get(invokeLatencyHeaderPrefix+"enabled"))
						w.Header().Set("Content-Type", "application/json")
						if protocol == "responses" {
							_, _ = io.WriteString(w,
								`{"output":[{"content":[{"type":"output_text","text":"route-result"}]}]}`)
						} else {
							_, _ = io.WriteString(w, `{"result":"route-result"}`)
						}
					}))
					defer server.Close()
					args := []string{"--local", "--port", strconv.Itoa(testPort(t, server.URL))}
					if explicitProtocol {
						args = append(args, "--protocol", protocol)
					}
					if flag != "" {
						args = append(args, flag)
					}
					args = append(args, "hello")
					cmd := newInvokeCommand(&azdext.ExtensionContext{NoPrompt: true})
					cmd.SetArgs(args)
					cmd.SetOut(io.Discard)
					cmd.SetErr(io.Discard)
					output, err := captureStdout(t, cmd.Execute)
					if flag == "--debug-latency=true" || flag == "--debug-latency" {
						requireLatencyRouteConflict(t, err)
						assert.Zero(t, posts.Load(), "explicit diagnostics must fail before local invocation")
						assert.NotContains(t, output, "Client elapsed:")
					} else {
						require.NoError(t, err)
						assert.EqualValues(t, 1, posts.Load())
						assert.Contains(t, output, "route-result")
					}
					assert.NotContains(t, output, "Platform latency")
				})
			}
		}
	}
}

func TestInvokeLatencyExplicitA2A(t *testing.T) {
	for _, target := range [][]string{
		{"--protocol", "a2a"},
		{"--agent-endpoint", "https://acct.services.ai.azure.com/api/projects/project/" +
			"agents/agent/endpoint/protocols/a2a?api-version=v1"},
	} {
		t.Run(target[0], func(t *testing.T) {
			cmd := newInvokeCommand(&azdext.ExtensionContext{NoPrompt: true})
			cmd.SetArgs(append(target, "--debug-latency=true", "hello"))
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			requireLatencyRouteConflict(t, cmd.Execute())
		})
	}
}

func TestInvokeLatencyResolvedA2A(t *testing.T) {
	for _, flag := range []struct {
		name     string
		enabled  bool
		explicit bool
	}{
		{name: "omitted", enabled: true},
		{name: "explicit false", explicit: true},
		{name: "explicit true", enabled: true, explicit: true},
	} {
		t.Run(flag.name, func(t *testing.T) {
			project := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{"agent": {
					Name: "agent", Host: AiAgentHost,
					AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "agent"}),
				}},
			}}
			address := newInvokeRemoteContextTestAzdServer(t, project, &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "test"},
			})
			t.Setenv("AZD_SERVER", address)
			client := newInvokeTestAzdClient(t, newInvokeUserConfigServer())
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/agents/agent/endpoint/protocols/a2a", r.URL.Path)
				assert.Empty(t, r.Header.Get(invokeLatencyHeaderPrefix+"enabled"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"test","result":{`+
					`"kind":"message","role":"agent","parts":[{"kind":"text","text":"route-result"}]}}`)
			}))
			defer server.Close()
			action := &InvokeAction{
				flags:    &invokeFlags{name: "agent", message: "hello", debugLatency: flag.enabled},
				noPrompt: true, debugLatencyExplicit: flag.explicit, credential: responseTestCredential{},
				resolvedRemoteContext: &remoteContext{
					name: "agent", projectEndpoint: server.URL, apiVersion: "v1", azdClient: client,
					invocableProtocols: []agent_api.AgentProtocol{agent_api.AgentProtocolA2A},
				},
			}
			output, err := captureStdout(t, func() error { return action.Run(t.Context()) })
			if flag.explicit && flag.enabled {
				requireLatencyRouteConflict(t, err)
				assert.Zero(t, posts.Load())
				assert.Nil(t, action.resolvedRemoteContext.azdClient, "resolved client must be closed on rejection")
			} else {
				require.NoError(t, err)
				assert.EqualValues(t, 1, posts.Load())
				assert.Contains(t, output, "route-result")
			}
			assert.NotContains(t, output, "Platform latency")
		})
	}
}

type latencyPromptAccountServer struct {
	azdext.UnimplementedAccountServiceServer
	calls atomic.Int32
}

func (s *latencyPromptAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	s.calls.Add(1)
	return nil, status.Error(codes.Unavailable, "test authentication boundary")
}

func TestInvokeLatencyPromptRoute(t *testing.T) {
	for _, protocol := range []string{"", "responses"} {
		for _, flag := range []string{"", "--debug-latency=false", "--debug-latency=true", "--debug-latency"} {
			t.Run("protocol="+protocol+"/"+flag, func(t *testing.T) {
				project := &helpersProjectServer{project: &azdext.ProjectConfig{
					Path: t.TempDir(),
					Services: map[string]*azdext.ServiceConfig{"agent": {
						Name: "agent", Host: AiAgentHost,
						AdditionalProperties: mustStruct(t, map[string]any{
							"kind": "prompt", "name": "agent", "model": "test-model", "instructions": "Be helpful.",
						}),
					}},
				}}
				account := &latencyPromptAccountServer{}
				address := newInvokeRemoteContextTestAzdServer(t, project, &promptEnvironmentServer{
					values: map[string]string{
						"AZURE_SUBSCRIPTION_ID":    "subscription",
						"AZURE_RESOURCE_GROUP":     "resource-group",
						"FOUNDRY_PROJECT_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/project",
					},
				}, account)
				t.Setenv("AZD_SERVER", address)
				args := []string{"hello"}
				if protocol != "" {
					args = append(args, "--protocol", protocol)
				}
				if flag != "" {
					args = append(args, flag)
				}
				cmd := newInvokeCommand(&azdext.ExtensionContext{NoPrompt: true})
				cmd.SetArgs(args)
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				_, err := captureStdout(t, cmd.Execute)
				if flag == "--debug-latency=true" || flag == "--debug-latency" {
					requireLatencyRouteConflict(t, err)
					assert.Zero(t, account.calls.Load(), "reject before prompt-agent authentication")
				} else {
					// Stop at the existing auth boundary rather than invoke a real prompt agent.
					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok, "expected the test authentication error, got %v", err)
					assert.Equal(t, exterrors.CodeTenantLookupFailed, localErr.Code)
					assert.Contains(t, err.Error(), "test authentication boundary")
					assert.EqualValues(t, 1, account.calls.Load())
				}
			})
		}
	}
}

func requireLatencyRouteConflict(t *testing.T, err error) {
	t.Helper()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected a structured argument conflict, got %v", err)
	assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
	assert.Contains(t, localErr.Message, "--debug-latency=true requires a remote Hosted Agent")
	assert.Contains(t, localErr.Suggestion, "--debug-latency=false")
}
