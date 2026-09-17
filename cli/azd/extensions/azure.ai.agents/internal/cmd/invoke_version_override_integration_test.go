// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const versionOverrideSource = " \n{\"input\":\"source input\",\"metadata\":{\"note\":\"keep unchanged\"}}\n "

const versionOverrideCreated = "event: response.created\ndata: " +
	`{"response":{"id":"resp_override","status":"in_progress"}}` + "\n\n"

const versionOverrideStream = versionOverrideCreated +
	"event: response.output_text.delta\ndata: " + `{"delta":"override-result"}` + "\n\n" +
	"event: response.completed\ndata: " +
	`{"response":{"id":"resp_override","status":"completed"}}` + "\n\n"

// These tests replace process stdout and sometimes environment variables; do not run them in parallel.
func TestInvokeVersionOverrideRemoteIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, protocol := range []string{"responses", "invocations"} {
		for _, format := range []string{outputDefault, outputRaw} {
			for _, tt := range []struct {
				name       string
				requested  string
				resolved   string
				fallback   string
				status     int
				fromFile   bool
				legacyOnly bool
				wantErr    string
			}{
				{name: "matched", requested: "4", resolved: "4", status: http.StatusOK},
				{name: "file input", requested: "4", resolved: "4", status: http.StatusOK, fromFile: true},
				{name: "legacy state only", requested: "4", resolved: "4", status: http.StatusOK, legacyOnly: true},
				{name: "latest", requested: "latest", resolved: "7", status: http.StatusOK},
				{
					name: "mismatch", requested: "4", resolved: "3", status: http.StatusOK,
					wantErr: "does not match requested version",
				},
				{
					name: "missing", requested: "4", status: http.StatusOK,
					wantErr: "expected exactly one x-agent-version-resolved header",
				},
				{
					name: "fallback", requested: "4", resolved: "4", fallback: "true", status: http.StatusOK,
					wantErr: "the service reported a version fallback",
				},
				{
					name: "multiple choices without evidence", requested: "4", status: http.StatusMultipleChoices,
					wantErr: "unexpected HTTP status 300",
				},
				{
					name: "final redirect without evidence", requested: "4", status: http.StatusFound,
					wantErr: "unexpected HTTP status 302",
				},
				{
					name: "final redirect with matching version", requested: "4", resolved: "4",
					status: http.StatusTemporaryRedirect, wantErr: "unexpected HTTP status 307",
				},
				{
					name: "permanent redirect with matching version", requested: "4", resolved: "4",
					status: http.StatusPermanentRedirect, wantErr: "unexpected HTTP status 308",
				},
				{name: "bad request", requested: "4", status: http.StatusBadRequest},
				{
					name: "conflict", requested: "4", resolved: "3", fallback: "true", status: http.StatusConflict,
				},
			} {
				t.Run(protocol+"/"+format+"/"+tt.name, func(t *testing.T) {
					body := `{"result":"override-result"}`
					if protocol == "responses" {
						body = versionOverrideStream
					}
					if tt.status >= http.StatusBadRequest {
						body = `{"error":{"code":"original_service_error","message":"candidate unavailable"}}`
					}
					flags := &invokeFlags{
						message: versionOverrideSource, protocol: protocol,
						outputFmt: format, versionOverride: tt.requested,
					}
					if tt.fromFile {
						flags.message = ""
						flags.inputFile = filepath.Join(t.TempDir(), "request.json")
						require.NoError(t, os.WriteFile(flags.inputFile, []byte(versionOverrideSource), 0o600))
					}
					fixture := newVersionOverrideHTTPFixture(t, flags, versionOverrideHTTPReply{
						status: tt.status, resolved: tt.resolved, fallback: tt.fallback, body: body,
					}, nil)
					if tt.legacyOnly {
						for _, field := range []string{"sessions", "conversations"} {
							fixture.config.setJSON(t, configPath(field), map[string]string{"agent": "legacy-" + field})
						}
						fixture.before = versionOverrideConfigSnapshot(fixture.config)
					}
					output, err := fixture.invoke(t)

					// All assertions, including recorded HTTP requests, run after stdout restoration.
					fixture.assertIsolated(t, 0)
					if tt.fromFile {
						source, readErr := os.ReadFile(flags.inputFile)
						require.NoError(t, readErr)
						assert.Equal(t, versionOverrideSource, string(source))
					}
					switch {
					case tt.status >= http.StatusBadRequest:
						require.ErrorContains(t, err, fmt.Sprintf("HTTP %d: %d %s",
							tt.status, tt.status, http.StatusText(tt.status)))
						assert.Contains(t, err.Error(), "POST "+fixture.action.resolvedRemoteContext.projectEndpoint)
						assert.NotContains(t, err.Error(), "could not be verified")
						if format == outputDefault {
							assert.Contains(t, err.Error(), body)
						}
					case tt.wantErr != "":
						requireVersionOverrideVerificationFailure(t, err, tt.wantErr)
						if format == outputDefault {
							assert.NotContains(t, output, "Invocation:")
							assert.NotContains(t, output, "sess_override")
						}
					default:
						require.NoError(t, err)
						assert.Contains(t, output, "override-result")
						if format == outputDefault {
							assert.Contains(t, output, "Version override: "+tt.requested+"; resolved: "+tt.resolved)
							assert.Contains(t, output, "Client elapsed:")
						}
					}
					if err != nil {
						assert.NotContains(t, output, "Client elapsed:")
						assert.NotContains(t, output, "Version override:")
						if format == outputDefault {
							assert.NotContains(t, output, "override-result")
							assert.NotContains(t, output, "Response:")
						}
					}
					if format == outputRaw {
						assert.Contains(t, output,
							fmt.Sprintf("HTTP/1.1 %d %s\r\n", tt.status, http.StatusText(tt.status)))
						if tt.resolved != "" {
							assert.Contains(t, output, "X-Agent-Version-Resolved: "+tt.resolved+"\r\n")
						}
						assert.True(t, strings.HasSuffix(output, "\r\n\r\n"+body), "raw body must remain verbatim")
						assert.NotContains(t, output, "Version override:")
						assert.NotContains(t, output, "Session:")
						assert.NotContains(t, output, "Invocation:")
						assert.NotContains(t, output, "Client elapsed:")
					}
				})
			}
		}
	}
}

func TestInvokeVersionOverrideInvocationsPollingIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	previousInterval := defaultLROPollInterval
	defaultLROPollInterval = time.Millisecond
	t.Cleanup(func() { defaultLROPollInterval = previousInterval })

	for _, format := range []string{outputDefault, outputRaw} {
		for _, tt := range []struct {
			name     string
			resolved string
			fallback string
			wantErr  string
		}{
			{name: "matched", resolved: "4"},
			{name: "mismatch", resolved: "3", wantErr: "does not match requested version"},
			{name: "missing", wantErr: "expected exactly one x-agent-version-resolved header"},
			{name: "fallback", resolved: "4", fallback: "true", wantErr: "the service reported a version fallback"},
		} {
			t.Run(format+"/"+tt.name, func(t *testing.T) {
				const accepted = `{"invocation_id":"inv_override","status":"accepted"}`
				const completed = `{"status":"completed","result":"override-result"}`
				fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
					message: versionOverrideSource, protocol: "invocations", versionOverride: "4", outputFmt: format,
				}, versionOverrideHTTPReply{
					status: http.StatusAccepted, resolved: tt.resolved, fallback: tt.fallback, body: accepted,
				}, &versionOverrideHTTPReply{status: http.StatusOK, body: completed})
				output, err := fixture.invoke(t)
				wantPolls := 0
				if tt.wantErr != "" {
					requireVersionOverrideVerificationFailure(t, err, tt.wantErr)
					assert.NotContains(t, output, "Polling for result")
					assert.NotContains(t, output, "override-result")
					assert.NotContains(t, output, "Client elapsed:")
					if format == outputDefault {
						assert.NotContains(t, output, "Invocation:")
						assert.NotContains(t, output, "inv_override")
					}
				} else {
					require.NoError(t, err, "poll responses need not repeat version-resolution headers")
					wantPolls = 1
					assert.Contains(t, output, "override-result")
					if format == outputRaw {
						assert.Contains(t, output, "HTTP/1.1 200 OK\r\n")
						assert.Contains(t, output, completed)
					} else {
						assert.Contains(t, output, "Invocation completed.")
					}
				}
				fixture.assertIsolated(t, wantPolls)
				if format == outputRaw {
					assert.Contains(t, output, "HTTP/1.1 202 Accepted\r\n")
					assert.Contains(t, output, accepted)
					assert.NotContains(t, output, "Version override:")
				}
			})
		}
	}
}

func TestInvokeVersionOverrideBackgroundResponsesIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, noWait := range []bool{false, true} {
		for _, tt := range []struct {
			name     string
			resolved string
			fallback string
			body     string
			wantErr  string
		}{
			{name: "matched", resolved: "4", body: versionOverrideStream},
			{name: "disconnected", resolved: "4", body: versionOverrideCreated},
			{
				name: "mismatch", resolved: "3", body: versionOverrideStream,
				wantErr: "does not match requested version",
			},
			{
				name: "missing", body: versionOverrideStream,
				wantErr: "expected exactly one x-agent-version-resolved header",
			},
			{
				name: "fallback", resolved: "4", fallback: "true", body: versionOverrideStream,
				wantErr: "the service reported a version fallback",
			},
		} {
			t.Run(fmt.Sprintf("no-wait=%t/%s", noWait, tt.name), func(t *testing.T) {
				fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
					message: versionOverrideSource, protocol: "responses",
					versionOverride: "4", outputFmt: outputDefault,
					longRunning: true, noWait: noWait,
				}, versionOverrideHTTPReply{
					status: http.StatusOK, resolved: tt.resolved, fallback: tt.fallback, body: tt.body,
				}, nil)
				output, err := fixture.invoke(t)
				fixture.assertIsolated(t, 0)
				const follow = `azd ai agent invocations follow --id "resp_override"` +
					` --protocol responses --agent-name "agent-service"`
				switch {
				case tt.wantErr != "":
					requireVersionOverrideVerificationFailure(t, err, tt.wantErr)
					assert.NotContains(t, output, "Response:")
					assert.NotContains(t, output, "resp_override")
					assert.NotContains(t, output, "override-result")
					assert.NotContains(t, output, "Next:")
					assert.NotContains(t, output, "Client elapsed:")
				case noWait:
					require.NoError(t, err)
					assert.Contains(t, output, "Response:     resp_override")
					assert.Contains(t, output, "Next:\n  "+follow)
					assert.NotContains(t, output, "override-result", "stop before consuming output after the ID")
				case tt.name == "disconnected":
					require.ErrorIs(t, err, errResponsesStreamDisconnected)
					assert.Contains(t, err.Error(), follow)
				default:
					require.NoError(t, err)
					assert.Contains(t, output, "Response:     resp_override")
					assert.Contains(t, output, "override-result")
				}
				assert.NotContains(t, output, "--current")
				if err != nil {
					assert.NotContains(t, err.Error(), "--current")
				}
			})
		}
	}
}

func TestInvokeVersionOverridePromptRouteIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, protocol := range []string{"", "responses", "invocations"} {
		t.Run("protocol="+protocol, func(t *testing.T) {
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
			args := []string{"--version-override", "4", "hello"}
			if protocol != "" {
				args = append(args, "--protocol", protocol)
			}
			cmd := newInvokeCommand(&azdext.ExtensionContext{NoPrompt: true})
			cmd.SetArgs(args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			output, err := captureStdout(t, cmd.Execute)
			requireVersionOverrideRouteConflict(t, err)
			assert.Zero(t, account.calls.Load(), "reject before prompt-agent authentication")
			assert.Empty(t, output)
		})
	}
}

func TestInvokeVersionOverrideResolvedA2ARouteIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
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
	fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
		name: "agent", message: versionOverrideSource, versionOverride: "4",
	}, versionOverrideHTTPReply{status: http.StatusBadRequest}, nil)
	fixture.action.noPrompt = true
	fixture.action.resolvedRemoteContext.invocableProtocols = []agent_api.AgentProtocol{agent_api.AgentProtocolA2A}
	output, err := captureStdout(t, func() error { return fixture.action.Run(t.Context()) })
	requireVersionOverrideRouteConflict(t, err)
	assert.Empty(t, output)
	assert.Empty(t, fixture.recordedRequests(), "resolved A2A must be rejected before HTTP invocation")
	assert.Nil(t, fixture.action.resolvedRemoteContext.azdClient, "close the resolved client on rejection")
	assert.Equal(t, fixture.before, versionOverrideConfigSnapshot(fixture.config))
}

func TestInvokeVersionOverrideHeadersIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, protocol := range []string{"responses", "invocations"} {
		for _, enabled := range []bool{false, true} {
			for _, format := range []string{outputDefault, outputRaw} {
				t.Run(fmt.Sprintf("%s/%s/debug-latency=%t", protocol, format, enabled), func(t *testing.T) {
					flags := &invokeFlags{
						message: versionOverrideSource, protocol: protocol, versionOverride: "4", outputFmt: format,
						debugLatency: enabled, userIdentityFlags: userIdentityFlags{userIdentity: "override-user"},
						clientHeaders: []string{
							"x-client-request-id: supplied-id", "x-client-tag: first", "x-client-tag: second",
						},
					}
					body := `{"result":"override-result"}`
					if protocol == "responses" {
						body = versionOverrideStream
					}
					fixture := newVersionOverrideHTTPFixture(t, flags, versionOverrideHTTPReply{
						status: http.StatusOK, resolved: "4", body: body,
					}, nil)
					headers, err := parseCustomHeaders(flags.clientHeaders)
					require.NoError(t, err)
					fixture.action.clientHeaders = headers
					output, err := fixture.invoke(t)

					require.NoError(t, err)
					assert.Contains(t, output, "override-result")
					fixture.assertIsolated(t, 0)
					for _, request := range fixture.recordedRequests() {
						assert.Equal(t, []string{"override-user"}, request.header.Values("x-ms-user-identity"))
						if request.path == fixture.postPath {
							assert.Equal(t, []string{"supplied-id"}, request.header.Values("x-client-request-id"))
							assert.Equal(t, []string{"first", "second"}, request.header.Values("x-client-tag"))
						}
						if enabled && request.path == fixture.postPath {
							assert.Equal(t, []string{"true"}, request.header.Values("x-ms-debug-latency-enabled"))
						} else {
							assert.NotContains(t, request.header, http.CanonicalHeaderKey("x-ms-debug-latency-enabled"))
						}
					}
				})
			}
		}
	}
}

func TestInvokeVersionOverrideConversationFailureIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, format := range []string{outputDefault, outputRaw} {
		t.Run(format, func(t *testing.T) {
			const failure = `{"error":{"message":"conversation unavailable"}}`
			fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
				message: versionOverrideSource, protocol: "responses", versionOverride: "4", outputFmt: format,
			}, versionOverrideHTTPReply{
				status: http.StatusOK, resolved: "4", body: versionOverrideStream,
				conversationStatus: http.StatusServiceUnavailable, conversationBody: failure,
			}, nil)
			output, err := fixture.invoke(t)

			require.ErrorContains(t, err, "HTTP 503: 503 Service Unavailable")
			assert.Contains(t, err.Error(), failure)
			assert.NotContains(t, err.Error(), "could not be verified")
			assert.Empty(t, output)
			requests := fixture.recordedRequests()
			require.Len(t, requests, 1, "conversation failure must prevent the invocation POST")
			request := requests[0]
			assert.NoError(t, request.err)
			assert.Equal(t, http.MethodPost, request.method)
			assert.Equal(t, "/agents/agent/endpoint/protocols/openai/conversations", request.path)
			assert.Equal(t, url.Values{"api-version": {"v1"}}, request.query)
			assert.Equal(t, "Bearer test-token", request.header.Get("Authorization"))
			assert.NotContains(t, request.header, http.CanonicalHeaderKey("x-agent-version-override"))
			assert.Zero(t, fixture.config.writes.Load())
			assert.Equal(t, fixture.before, versionOverrideConfigSnapshot(fixture.config))
		})
	}
}

func TestInvokeVersionOverrideExplicitEndpointIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, protocol := range []string{"responses", "invocations"} {
		t.Run(protocol, func(t *testing.T) {
			flags := &invokeFlags{
				message: versionOverrideSource, protocol: protocol, versionOverride: "4", outputFmt: outputDefault,
			}
			body := `{"result":"override-result"}`
			if protocol == "responses" {
				body = versionOverrideStream
			}
			fixture := newVersionOverrideHTTPFixture(t, flags, versionOverrideHTTPReply{
				status: http.StatusOK, resolved: "4", body: body,
			}, nil)
			server := grpc.NewServer()
			azdext.RegisterUserConfigServiceServer(server, fixture.config)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				_ = listener.Close()
			})
			t.Setenv("AZD_SERVER", listener.Addr().String())
			flags.agentEndpoint = "https://acct.services.ai.azure.com/api/projects/project" +
				fixture.postPath + "?api-version=v1"
			parsed, err := parseAgentEndpoint(flags.agentEndpoint)
			require.NoError(t, err)
			// Keep URL parsing real, but route all HTTP to the fixture, never the Foundry domain.
			parsed.ProjectEndpoint = fixture.action.resolvedRemoteContext.projectEndpoint
			flags.name, flags.protocol = parsed.AgentName, string(parsed.Protocol)
			action := &InvokeAction{
				flags: flags, endpoint: parsed, credential: responseTestCredential{}, noPrompt: true,
			}
			// No cached remoteContext: Run must resolve the endpoint and attach to the mock daemon.
			output, err := captureStdout(t, func() error { return action.Run(t.Context()) })

			require.NoError(t, err)
			assert.Contains(t, output, "override-result")
			assert.Contains(t, output, "Version override: 4; resolved: 4")
			assert.Nil(t, action.resolvedRemoteContext)
			fixture.assertIsolated(t, 0)
		})
	}
}

// Regression for #10079: prompt detection and hosted invocation must share one
// service selection, without replacing a positional name with a service key.
func TestInvokeVersionOverrideServiceSelectionIntegration(t *testing.T) {
	const promptProjectEndpoint = "https://acct.services.ai.azure.com/api/projects/project"
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AGENT_DEFINITION_PATH", "")
	for _, protocol := range []string{"invocations", "responses"} {
		for _, automatic := range []bool{false, true} {
			for _, tt := range []struct {
				name         string
				agentName    string
				noPrompt     bool
				selectPrompt bool
				wantPickers  int32
			}{
				{name: "selected hosted", wantPickers: 1},
				{name: "ambiguous no-prompt", noPrompt: true},
				{name: "explicit service", agentName: "z-hosted"},
				{name: "deployed name", agentName: "agent"},
				{name: "selected prompt", selectPrompt: true, wantPickers: 1},
			} {
				t.Run(fmt.Sprintf("%s/automatic=%t/%s", protocol, automatic, tt.name), func(t *testing.T) {
					fixtureFlags := &invokeFlags{
						message: versionOverrideSource, protocol: protocol, versionOverride: "4", outputFmt: outputDefault,
					}
					body := `{"result":"override-result"}`
					if protocol == "responses" {
						body = versionOverrideStream
					}
					fixture := newVersionOverrideHTTPFixture(t, fixtureFlags, versionOverrideHTTPReply{
						status: http.StatusOK, resolved: "4", body: body,
					}, nil)
					projectEndpoint := fixture.action.resolvedRemoteContext.projectEndpoint
					endpoint := projectEndpoint + fixture.postPath + "?api-version=v1"
					otherProject := projectEndpoint + "/other-project"
					otherEndpoint := otherProject + strings.Replace(fixture.postPath, "/agents/agent/", "/agents/other/", 1)
					project := &helpersProjectServer{project: &azdext.ProjectConfig{
						Path: t.TempDir(),
						Services: map[string]*azdext.ServiceConfig{
							"a-other": {
								Name: "a-other", Host: AiAgentHost,
								AdditionalProperties: mustStruct(t, map[string]any{
									"kind": "prompt", "name": "other", "model": "test-model", "instructions": "Be helpful.",
								}),
							},
							"z-hosted": {
								Name: "z-hosted", Host: AiAgentHost,
								AdditionalProperties: mustStruct(t, map[string]any{"kind": "hosted", "name": "agent"}),
							},
						},
					}}
					values := map[string]string{
						"AZURE_SUBSCRIPTION_ID":                     "subscription",
						"AZURE_RESOURCE_GROUP":                      "resource-group",
						"FOUNDRY_PROJECT_ENDPOINT":                  promptProjectEndpoint,
						"AGENT_Z_HOSTED_NAME":                       "agent",
						"AGENT_Z_HOSTED_VERSION":                    "3",
						"AGENT_Z_HOSTED_PROJECT_ENDPOINT":           projectEndpoint,
						"AGENT_Z_HOSTED_ENDPOINT":                   endpoint,
						"AGENT_Z_HOSTED_PROTOCOL_ENDPOINTS_VERSION": "1",
						"AGENT_A_OTHER_NAME":                        "other",
						"AGENT_A_OTHER_VERSION":                     "3",
						"AGENT_A_OTHER_PROJECT_ENDPOINT":            otherProject,
						"AGENT_A_OTHER_ENDPOINT":                    otherEndpoint + "?api-version=v1",
						"AGENT_A_OTHER_PROTOCOL_ENDPOINTS_VERSION":  "1",
					}
					values["AGENT_Z_HOSTED_"+strings.ToUpper(protocol)+"_ENDPOINT"] = values["AGENT_Z_HOSTED_ENDPOINT"]
					values["AGENT_A_OTHER_"+strings.ToUpper(protocol)+"_ENDPOINT"] = values["AGENT_A_OTHER_ENDPOINT"]
					// Even an erroneous project-endpoint fallback must stay offline. Prompt
					// validation uses the valid Foundry URL in the mocked environment above.
					t.Setenv("FOUNDRY_PROJECT_ENDPOINT", projectEndpoint+"/unexpected-project")
					picker := &versionOverrideRotatingPromptServer{firstIndex: 1}
					if tt.selectPrompt {
						picker.firstIndex = 0
					}
					account := &latencyPromptAccountServer{}
					server := grpc.NewServer()
					azdext.RegisterProjectServiceServer(server, project)
					azdext.RegisterEnvironmentServiceServer(server, &promptEnvironmentServer{values: values})
					azdext.RegisterPromptServiceServer(server, picker)
					azdext.RegisterUserConfigServiceServer(server, fixture.config)
					azdext.RegisterAccountServiceServer(server, account)
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					require.NoError(t, err)
					go func() { _ = server.Serve(listener) }()
					t.Cleanup(func() {
						server.Stop()
						_ = listener.Close()
					})
					t.Setenv("AZD_SERVER", listener.Addr().String())
					flags := *fixtureFlags
					flags.name = tt.agentName
					if automatic {
						flags.protocol = ""
					}
					// Reuse only the fixture's server/config, never its cached remoteContext.
					action := &InvokeAction{flags: &flags, credential: responseTestCredential{}, noPrompt: tt.noPrompt}
					output, err := captureStdout(t, func() error { return action.Run(t.Context()) })

					// Assertions run only after stdout is restored, including on regressions.
					assert.Equal(t, tt.wantPickers, picker.calls.Load(), "do not repeat the service picker")
					assert.Equal(t, tt.agentName, flags.name, "preserve positional-name semantics")
					assert.Zero(t, account.calls.Load(), "never authenticate a rejected prompt agent")
					assert.Zero(t, fixture.config.writes.Load())
					assert.Equal(t, fixture.before, versionOverrideConfigSnapshot(fixture.config))
					if tt.noPrompt || tt.selectPrompt {
						assert.Empty(t, fixture.recordedRequests(), "reject before any HTTP request")
						assert.Empty(t, output)
						if tt.selectPrompt {
							requireVersionOverrideRouteConflict(t, err)
						} else {
							require.ErrorContains(t, err,
								"multiple azure.ai.agent services found in azure.yaml: a-other, z-hosted")
							assert.Contains(t, err.Error(), "Provide the service name as a positional argument")
						}
						return
					}
					// Exact fixture requests prove the selected project, deployed name and
					// protocol, override header, fresh state, and absence of OpenAPI/cache writes.
					fixture.assertIsolated(t, 0)
					require.NoError(t, err)
					assert.Contains(t, output, "override-result")
					assert.Contains(t, output, "Version override: 4; resolved: 4")
					if tt.wantPickers == 1 {
						assert.Equal(t, "z-hosted", action.protocolServiceName, "cache the service key, not deployed name")
					}
				})
			}
		}
	}
}

type versionOverrideRotatingPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	firstIndex int32
	calls      atomic.Int32
}

func (s *versionOverrideRotatingPromptServer) Select(
	context.Context, *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	index := int32(0) // A second picker switches to the first sorted service, a-other.
	if s.calls.Add(1) == 1 {
		index = s.firstIndex
	}
	return &azdext.SelectResponse{Value: new(index)}, nil
}

type versionOverrideUserConfigServer struct {
	*invokeUserConfigServer
	writes atomic.Int32
}

func (s *versionOverrideUserConfigServer) Set(
	ctx context.Context, req *azdext.SetUserConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.writes.Add(1)
	return s.invokeUserConfigServer.Set(ctx, req)
}

type versionOverrideHTTPReply struct {
	status             int
	resolved           string
	fallback           string
	body               string
	conversationStatus int
	conversationBody   string
}

type versionOverrideHTTPRequest struct {
	method string
	path   string
	query  url.Values
	header http.Header
	body   []byte
	err    error
}

type versionOverrideHTTPFixture struct {
	action   *InvokeAction
	config   *versionOverrideUserConfigServer
	before   map[string][]byte
	postPath string
	mu       sync.Mutex
	requests []versionOverrideHTTPRequest
}

func newVersionOverrideHTTPFixture(
	t *testing.T, flags *invokeFlags, initial versionOverrideHTTPReply, poll *versionOverrideHTTPReply,
) *versionOverrideHTTPFixture {
	t.Helper()
	fixture := &versionOverrideHTTPFixture{
		config:   &versionOverrideUserConfigServer{invokeUserConfigServer: newInvokeUserConfigServer()},
		postPath: "/agents/agent/endpoint/protocols/invocations",
	}
	if flags.protocol == "responses" {
		fixture.postPath = "/agents/agent/endpoint/protocols/openai/responses"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		fixture.mu.Lock()
		fixture.requests = append(fixture.requests, versionOverrideHTTPRequest{
			method: r.Method, path: r.URL.Path, query: r.URL.Query(), header: r.Header.Clone(), body: body, err: err,
		})
		fixture.mu.Unlock()
		// Record rather than assert here: this handler runs while process stdout is captured.
		if r.Method == http.MethodPost && r.URL.Path == "/agents/agent/endpoint/protocols/openai/conversations" {
			w.Header().Set("Content-Type", "application/json")
			if initial.conversationStatus != 0 {
				w.WriteHeader(initial.conversationStatus)
				_, _ = io.WriteString(w, initial.conversationBody)
				return
			}
			_, _ = io.WriteString(w, `{"id":"conv_override"}`)
			return
		}
		reply := initial
		switch {
		case r.Method == http.MethodPost && r.URL.Path == fixture.postPath:
			w.Header().Set("x-agent-session-id", "sess_override")
			if flags.protocol == "invocations" {
				w.Header().Set("x-agent-invocation-id", "inv_override")
			}
		case r.Method == http.MethodGet && r.URL.Path == fixture.postPath+"/inv_override" && poll != nil:
			reply = *poll
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if flags.protocol == "responses" && reply.status < http.StatusBadRequest {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		if reply.resolved != "" {
			w.Header().Set("x-agent-version-resolved", reply.resolved)
			w.Header().Set("x-agent-version-resolution", "flightoverride")
		}
		if reply.fallback != "" {
			w.Header().Set("x-agent-version-fallback", reply.fallback)
		}
		w.WriteHeader(reply.status)
		_, _ = io.WriteString(w, reply.body)
	}))
	t.Cleanup(server.Close)
	agentKey := buildAgentKey(server.URL, "agent", "", false)
	for _, field := range []string{"sessions", "conversations"} {
		fixture.config.setJSON(t, configPath(field), map[string]string{
			agentKey: "ordinary-" + field, "agent": "legacy-" + field,
		})
	}
	fixture.config.setJSON(t, responsesConfigPath, map[string]savedResponse{
		agentKey: {ResponseID: "resp_previous"}, "agent": {ResponseID: "resp_legacy"},
	})
	fixture.config.setJSON(t, invocationsConfigPath, map[string]savedInvocation{
		agentKey: {InvocationID: "inv_previous"}, "agent": {InvocationID: "inv_legacy"},
	})
	fixture.before = versionOverrideConfigSnapshot(fixture.config)
	fixture.action = &InvokeAction{
		flags: flags, credential: responseTestCredential{},
		resolvedRemoteContext: &remoteContext{
			name: "agent", serviceName: "agent-service", projectEndpoint: server.URL, apiVersion: "v1",
			agentKey: agentKey, azdClient: newInvokeTestAzdClient(t, fixture.config),
		},
	}
	return fixture
}

func (f *versionOverrideHTTPFixture) invoke(t *testing.T) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		if f.action.flags.protocol == "responses" {
			return f.action.responsesRemote(t.Context())
		}
		return f.action.invocationsRemote(t.Context())
	})
}

func (f *versionOverrideHTTPFixture) recordedRequests() []versionOverrideHTTPRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *versionOverrideHTTPFixture) assertIsolated(t *testing.T, polls int) {
	t.Helper()
	assert.Zero(t, f.config.writes.Load(), "override invokes must not write persistent state")
	assert.Equal(t, f.before, versionOverrideConfigSnapshot(f.config),
		"ordinary and legacy sessions, conversations, Responses, and Invocations must remain unchanged")
	assert.NotEmpty(t, f.action.resolvedRemoteContext.agentKey, "isolation must not mutate the source context")
	wantRequests := []string{http.MethodPost + " " + f.postPath}
	if f.action.flags.protocol == "responses" {
		wantRequests = append([]string{http.MethodPost + " /agents/agent/endpoint/protocols/openai/conversations"},
			wantRequests...)
	}
	for range polls {
		wantRequests = append(wantRequests, http.MethodGet+" "+f.postPath+"/inv_override")
	}
	var gotRequests []string
	for _, request := range f.recordedRequests() {
		gotRequests = append(gotRequests, request.method+" "+request.path)
		assert.NoError(t, request.err)
		assert.Equal(t, "v1", request.query.Get("api-version"))
		assert.NotContains(t, request.query, "agent_session_id", "never reuse a stored session")
		assert.Equal(t, "Bearer test-token", request.header.Get("Authorization"))
		if request.method != http.MethodPost || request.path != f.postPath {
			assert.NotContains(t, request.header, http.CanonicalHeaderKey("x-agent-version-override"),
				"conversation creation and lifecycle GETs must not select a version")
			continue
		}
		assert.Equal(t, []string{f.action.flags.versionOverride}, request.header.Values("x-agent-version-override"))
		assert.Equal(t, "application/json", request.header.Get("Content-Type"))
		if f.action.flags.protocol == "responses" {
			var body map[string]any
			if assert.NoError(t, json.Unmarshal(request.body, &body)) {
				assert.Equal(t, versionOverrideSource, body["input"])
				assert.Equal(t, true, body["stream"])
				assert.Equal(t, map[string]any{"id": "conv_override"}, body["conversation"])
				assert.NotContains(t, body, "agent_session_id")
				assert.NotContains(t, body, "agent_reference", "version selection belongs only in the header")
				assert.Equal(t, f.action.flags.longRunning, body["background"] == true)
				assert.Equal(t, f.action.flags.longRunning, body["store"] == true)
			}
		} else {
			assert.Equal(t, versionOverrideSource, string(request.body), "preserve the source body byte for byte")
		}
	}
	assert.Equal(t, wantRequests, gotRequests, "no session creation, OpenAPI fetch, or unexpected poll")
}

func versionOverrideConfigSnapshot(config *versionOverrideUserConfigServer) map[string][]byte {
	config.mu.Lock()
	defer config.mu.Unlock()
	return maps.Clone(config.values)
}

func requireVersionOverrideVerificationFailure(t *testing.T, err error, reason string) {
	t.Helper()
	require.ErrorContains(t, err, reason)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "the action must return a structured failure, including in raw mode: %v", err)
	assert.Equal(t, exterrors.CodeAgentVersionVerificationFailed, localErr.Code)
	assert.Contains(t, localErr.Suggestion, "may already have executed")
}

func requireVersionOverrideRouteConflict(t *testing.T, err error) {
	t.Helper()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected a structured argument conflict, got %v", err)
	assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
	assert.Contains(t, localErr.Message, "--version-override requires a remote hosted agent")
	assert.Contains(t, localErr.Suggestion, "local, A2A, or prompt agents")
}
