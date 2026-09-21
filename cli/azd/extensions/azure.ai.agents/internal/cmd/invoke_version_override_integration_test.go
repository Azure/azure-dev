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
				name         string
				requested    string
				resolved     string
				fallback     string
				status       int
				streaming    bool
				fromFile     bool
				legacyOnly   bool
				debugLatency bool
				wantErr      string
			}{
				{name: "matched", resolved: "4"},
				{name: "debug latency", resolved: "4", debugLatency: true},
				{name: "file input", resolved: "4", fromFile: true},
				{name: "legacy state only", resolved: "4", legacyOnly: true},
				{name: "latest", requested: "latest", resolved: "7"},
				{name: "mismatch", resolved: "3", wantErr: "does not match requested version"},
				{name: "missing"},
				{name: "missing SSE", streaming: true},
				{name: "latest missing", requested: "latest"},
				{name: "malformed", resolved: "4,3"},
				{name: "unusable", resolved: "latest"},
				{name: "invalid fallback", resolved: "4", fallback: "unknown"},
				{name: "fallback", resolved: "4", fallback: "true", wantErr: "the service reported a version fallback"},
				{name: "multiple choices", status: http.StatusMultipleChoices},
				{name: "final redirect", status: http.StatusFound},
				{name: "temporary redirect matched", resolved: "4", status: http.StatusTemporaryRedirect},
				{name: "permanent redirect matched", resolved: "4", status: http.StatusPermanentRedirect},
				{name: "bad request", status: http.StatusBadRequest},
				{name: "conflict", resolved: "3", fallback: "true", status: http.StatusConflict},
			} {
				t.Run(protocol+"/"+format+"/"+tt.name, func(t *testing.T) {
					if tt.status == 0 {
						tt.status = http.StatusOK
					}
					if tt.requested == "" {
						tt.requested = "4"
					}
					body := `{"result":"override-result"}`
					contentType := "application/json"
					if protocol == "responses" {
						body = versionOverrideStream
					} else if tt.streaming {
						contentType = "text/event-stream"
						body = "data: " + body + "\n\ndata: [DONE]\n\n"
					}
					if tt.status >= http.StatusBadRequest {
						body = `{"error":{"code":"original_service_error","message":"candidate unavailable"}}`
					}
					flags := &invokeFlags{
						message: versionOverrideSource, protocol: protocol,
						outputFmt: format, versionOverride: tt.requested,
						debugLatency: tt.debugLatency, userIdentityFlags: userIdentityFlags{userIdentity: "override-user"},
						clientHeaders: []string{
							"x-client-request-id: supplied-id", "x-client-tag: first", "x-client-tag: second",
						},
					}
					if tt.fromFile {
						flags.message = ""
						flags.inputFile = filepath.Join(t.TempDir(), "request.json")
						require.NoError(t, os.WriteFile(flags.inputFile, []byte(versionOverrideSource), 0o600))
					}
					fixture := newVersionOverrideHTTPFixture(t, flags, versionOverrideHTTPReply{
						status: tt.status, resolved: tt.resolved, fallback: tt.fallback,
						body: body, contentType: contentType,
					}, nil)
					headers, err := parseCustomHeaders(flags.clientHeaders)
					require.NoError(t, err)
					fixture.action.clientHeaders = headers
					if tt.legacyOnly {
						for _, field := range []string{"sessions", "conversations"} {
							fixture.config.setJSON(t, configPath(field), map[string]string{"agent": "legacy-" + field})
						}
						fixture.before = versionOverrideConfigSnapshot(fixture.config)
					}
					output, err := fixture.invoke(t)

					// All assertions, including recorded HTTP requests, run after stdout restoration.
					fixture.assertIsolated(t, 0)
					for _, request := range fixture.recordedRequests() {
						if request.path == fixture.postPath {
							assert.Equal(t, []string{"supplied-id"}, request.header.Values("x-client-request-id"))
							assert.Equal(t, []string{"first", "second"}, request.header.Values("x-client-tag"))
						}
						if tt.debugLatency && request.path == fixture.postPath {
							assert.Equal(t, []string{"true"}, request.header.Values("x-ms-debug-latency-enabled"))
						} else {
							assert.NotContains(t, request.header, http.CanonicalHeaderKey("x-ms-debug-latency-enabled"))
						}
					}
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
						_, structured := errors.AsType[*azdext.LocalError](err)
						assert.False(t, structured, "preserve the original HTTP error")
						if format == outputDefault {
							assert.Contains(t, err.Error(), body)
						}
					case tt.status >= http.StatusMultipleChoices:
						require.ErrorContains(t, err, fmt.Sprintf("unexpected HTTP status %d", tt.status))
						_, structured := errors.AsType[*azdext.LocalError](err)
						assert.False(t, structured, "non-2xx is an HTTP failure, not a routing failure")
					case tt.wantErr != "":
						requireVersionOverrideRoutingFailure(t, err, tt.wantErr)
					default:
						require.NoError(t, err)
					}
					if tt.status == http.StatusOK {
						assert.Contains(t, output, "override-result", "normal handling continues even on a routing mismatch")
						if format == outputDefault {
							if protocol == "responses" {
								assert.Contains(t, output, "Response:     resp_override")
							} else {
								assert.Contains(t, output, "Invocation:   inv_override")
							}
							resolved := tt.resolved
							if resolved == "" || tt.name == "malformed" || tt.name == "unusable" {
								resolved = "not reported"
							}
							assert.Contains(t, output, "Version override: "+tt.requested+"; resolved: "+resolved)
							assert.Contains(t, output, "Client elapsed:")
							if err != nil {
								assert.NotContains(t, output, "Next:")
							}
						}
					} else {
						assert.NotContains(t, output, "Client elapsed:")
						assert.NotContains(t, output, "Version override:")
						if format == outputDefault {
							assert.NotContains(t, output, "override-result")
							assert.NotContains(t, output, "Response:")
						}
					}
					assert.NotContains(t, output, "Warning:", "advisories belong on stderr, not in the reply")
					if format == outputRaw {
						assert.Contains(t, output,
							fmt.Sprintf("HTTP/1.1 %d %s\r\n", tt.status, http.StatusText(tt.status)))
						if tt.resolved != "" {
							assert.Contains(t, output, "X-Agent-Version-Resolved: "+tt.resolved+"\r\n")
						}
						if protocol == "invocations" {
							assert.Contains(t, output, "X-Agent-Invocation-Id: inv_override\r\n")
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
			name        string
			resolved    string
			fallback    string
			body        string
			omitHeader  bool
			wantID      string
			wantErr     string
			wantBodyErr string
		}{
			{name: "matched", resolved: "4", wantID: "inv_override"},
			{name: "missing", wantID: "inv_override"},
			{name: "malformed metadata", resolved: "4,3", fallback: "unknown", wantID: "inv_override"},
			{
				name: "missing metadata body-only identity", omitHeader: true, wantID: "inv_override",
				body: `{"invocation_id":"inv_override","status":"accepted"}`,
			},
			{
				name: "missing metadata malformed payload", omitHeader: true,
				body: `{"invocation_id":`, wantBodyErr: "received 202 Accepted but no invocation ID found",
			},
			{
				name: "mismatch", resolved: "3", wantID: "inv_override",
				wantErr: "does not match requested version",
			},
			{
				name: "fallback", resolved: "4", fallback: "true", wantID: "inv_override",
				wantErr: "the service reported a version fallback",
			},
			{
				name: "body-only identity", resolved: "3", omitHeader: true, wantID: "inv_body_only",
				body:    `{"invocation_id":"inv_body_only","status":"accepted"}`,
				wantErr: "does not match requested version",
			},
			{
				name: "header wins over conflicting body", resolved: "3", wantID: "inv_override",
				body:    `{"invocation_id":"inv_conflicting","status":"accepted"}`,
				wantErr: "does not match requested version",
			},
			{
				name: "missing body identity", resolved: "3", omitHeader: true,
				body: `{"status":"accepted"}`, wantErr: "does not match requested version",
			},
			{
				name: "empty body", resolved: "3", omitHeader: true,
				wantErr: "does not match requested version",
			},
			{
				name: "malformed body", resolved: "3", omitHeader: true,
				body: `{"invocation_id":"do-not-copy-this-payload"`, wantErr: "does not match requested version",
			},
		} {
			t.Run(format+"/"+tt.name, func(t *testing.T) {
				accepted := tt.body
				if accepted == "" && !tt.omitHeader {
					accepted = `{"invocation_id":"inv_override","status":"accepted"}`
				}
				const completed = `{"status":"completed","result":"override-result"}`
				fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
					message: versionOverrideSource, protocol: "invocations", versionOverride: "4", outputFmt: format,
				}, versionOverrideHTTPReply{
					status: http.StatusAccepted, resolved: tt.resolved, fallback: tt.fallback, body: accepted,
					omitInvocationIDHeader: tt.omitHeader,
				}, &versionOverrideHTTPReply{status: http.StatusOK, body: completed})
				output, err := fixture.invoke(t)
				wantPolls := 0
				if tt.wantBodyErr != "" {
					require.ErrorContains(t, err, tt.wantBodyErr)
					_, structured := errors.AsType[*azdext.LocalError](err)
					assert.False(t, structured)
				} else if tt.wantErr != "" {
					requireVersionOverrideRoutingFailure(t, err, tt.wantErr)
					assert.NotContains(t, output, "Polling for result")
					assert.NotContains(t, output, "override-result")
					assert.NotContains(t, output, "Client elapsed:")
					if format == outputRaw {
						assert.True(t, strings.HasSuffix(output, "\r\n\r\n"+accepted), "preserve the accepted body verbatim")
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
				if format == outputDefault {
					if tt.wantID != "" {
						assert.Contains(t, output, "Invocation:   "+tt.wantID, "the normal parser reports the ID")
					} else {
						assert.NotContains(t, output, "Invocation:")
					}
					assert.NotContains(t, output, "inv_conflicting")
				}
				fixture.assertIsolated(t, wantPolls)
				assert.NotContains(t, output, "Warning:")
				if format == outputRaw {
					if tt.wantBodyErr == "" {
						assert.Contains(t, output, "HTTP/1.1 202 Accepted\r\n")
						assert.Contains(t, output, accepted)
					}
					if !tt.omitHeader {
						assert.Contains(t, output, "X-Agent-Invocation-Id: inv_override\r\n")
					}
					assert.NotContains(t, output, "Version override:")
					assert.NotContains(t, output, "Invocation:")
					assert.NotContains(t, output, "azd ai agent invocations")
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
			{name: "missing", body: versionOverrideStream},
			{name: "malformed", resolved: "4,3", fallback: "unknown", body: versionOverrideStream},
			{name: "disconnected", body: versionOverrideCreated},
			{
				name: "mismatch", resolved: "3", body: versionOverrideStream,
				wantErr: "does not match requested version",
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
				assert.Contains(t, output, "Response:     resp_override", "use the normal SSE identity tracker")
				assert.NotContains(t, output, "Warning:")
				switch {
				case tt.wantErr != "":
					requireVersionOverrideRoutingFailure(t, err, tt.wantErr)
					assert.NotContains(t, output, "override-result")
					assert.Contains(t, output, "Next:\n  "+follow)
					assert.NotContains(t, output, "Client elapsed:")
				case noWait:
					require.NoError(t, err)
					assert.Contains(t, output, "Next:\n  "+follow)
					assert.NotContains(t, output, "override-result", "stop before consuming output after the ID")
				case tt.name == "disconnected":
					require.ErrorIs(t, err, errResponsesStreamDisconnected)
					assert.Contains(t, err.Error(), follow)
				default:
					require.NoError(t, err)
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

func TestInvokeVersionOverridePayloadFailuresIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, tt := range []struct {
		name        string
		protocol    string
		body        string
		contentType string
		wantErr     string
		wantID      string
		longRunning bool
	}{
		{
			name: "Responses agent error", protocol: "responses", wantID: "resp_override",
			body: versionOverrideCreated + "event: error\ndata: " +
				`{"code":"agent_failure","message":"candidate failed"}` + "\n\n",
			wantErr: "agent error (agent_failure): candidate failed",
		},
		{
			name: "Responses failed status", protocol: "responses", wantID: "resp_override",
			body: versionOverrideCreated + "event: response.failed\ndata: " +
				`{"response":{"id":"resp_override","status":"failed",` +
				`"error":{"code":"agent_failure","message":"candidate failed"}}}` + "\n\n",
			wantErr: "agent failed (agent_failure): candidate failed",
		},
		{
			name: "Responses malformed event after ID", protocol: "responses", wantID: "resp_override",
			body:    versionOverrideCreated + "event: response.output_text.delta\ndata: invalid-json\n\n",
			wantErr: "decode Responses SSE event",
		},
		{
			name: "background malformed event", protocol: "responses", longRunning: true,
			body: "event: response.created\ndata: invalid-json\n\n", wantErr: "decode Responses SSE event",
		},
		{
			name: "Invocations agent error", protocol: "invocations", wantID: "inv_override",
			body:    `{"error":{"code":"agent_failure","message":"candidate failed"}}`,
			wantErr: "agent error (agent_failure): candidate failed",
		},
		{
			name: "Invocations SSE error", protocol: "invocations", wantID: "inv_override", contentType: "text/event-stream",
			body:    "data: " + `{"error":{"code":"agent_failure","message":"candidate failed"}}` + "\n\n",
			wantErr: "agent error (agent_failure): candidate failed",
		},
		{
			name: "Invocations named SSE error", protocol: "invocations", wantID: "inv_override",
			contentType: "text/event-stream",
			body:        "event:error\r\ndata:candidate failed\r\n\r\n",
			wantErr:     "agent stream error: candidate failed",
		},
	} {
		for _, variant := range []struct {
			resolved string
			fallback string
			format   string
		}{
			{format: outputDefault}, {resolved: "3", format: outputDefault},
			{format: outputRaw}, {resolved: "3", format: outputRaw},
			{resolved: "4", format: outputDefault}, {resolved: "4", format: outputRaw},
			{resolved: "4", fallback: "true", format: outputDefault},
			{resolved: "4", fallback: "true", format: outputRaw},
		} {
			if variant.format == outputRaw && tt.longRunning {
				continue
			}
			t.Run(tt.name+"/"+variant.format+"/resolved="+variant.resolved+"/fallback="+variant.fallback, func(t *testing.T) {
				fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
					message: versionOverrideSource, protocol: tt.protocol, versionOverride: "4",
					outputFmt: variant.format, longRunning: tt.longRunning,
				}, versionOverrideHTTPReply{
					status: http.StatusOK, resolved: variant.resolved, body: tt.body, contentType: tt.contentType,
					fallback: variant.fallback,
				}, nil)
				output, err := fixture.invoke(t)

				require.ErrorContains(t, err, tt.wantErr, "HTTP 200 and missing metadata must not mask protocol failures")
				serialized := azdext.WrapError(err)
				require.NotNil(t, serialized)
				assert.Contains(t, serialized.Message, tt.wantErr, "the host must receive the agent failure")
				if variant.resolved != "3" && variant.fallback == "" {
					_, structured := errors.AsType[*azdext.LocalError](err)
					assert.False(t, structured, "missing or matching metadata adds no routing failure")
				} else {
					reason := "does not match requested version"
					if variant.fallback != "" {
						reason = "the service reported a version fallback"
					}
					requireVersionOverrideRoutingFailure(t, err, reason)
					assert.Contains(t, serialized.Message, reason)
				}
				if variant.format == outputRaw {
					assert.True(t, strings.HasSuffix(output, "\r\n\r\n"+tt.body))
					assert.NotContains(t, output, "Response:")
					assert.NotContains(t, output, "Invocation:")
				} else if tt.wantID != "" {
					label := "Invocation:   "
					if tt.protocol == "responses" {
						label = "Response:     "
					}
					assert.Contains(t, output, label+tt.wantID)
				}
				assert.NotContains(t, output, "Client elapsed:")
				assert.NotContains(t, output, "Next:")
				fixture.assertIsolated(t, 0)
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
			assert.NotContains(t, err.Error(), "was not honored")
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
		for _, tt := range []struct {
			name     string
			format   string
			mismatch bool
		}{
			{name: "matched", format: outputDefault},
			{name: "mismatch/default", format: outputDefault, mismatch: true},
			{name: "mismatch/raw", format: outputRaw, mismatch: true},
		} {
			t.Run(protocol+"/"+tt.name, func(t *testing.T) {
				flags := &invokeFlags{
					message: versionOverrideSource, protocol: protocol, versionOverride: "4", outputFmt: tt.format,
				}
				reply := versionOverrideHTTPReply{status: http.StatusOK, resolved: "4", body: `{"result":"override-result"}`}
				apiVersion := "v1"
				if protocol == "responses" {
					reply.body = versionOverrideStream
				}
				if tt.mismatch {
					reply.resolved = "3"
					flags.userIdentity = "override-user"
					apiVersion = "2026-09-17-preview"
					if protocol == "responses" {
						flags.longRunning = tt.format != outputRaw
					} else {
						reply.status = http.StatusAccepted
						reply.body = `{"invocation_id":"inv_override","status":"accepted"}`
					}
				}
				fixture := newVersionOverrideHTTPFixture(t, flags, reply, nil)
				fixture.action.resolvedRemoteContext.apiVersion = apiVersion
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
					fixture.postPath + "?api-version=" + apiVersion
				parsed, err := parseAgentEndpoint(flags.agentEndpoint)
				require.NoError(t, err)
				// Keep URL parsing real, but route all HTTP to the fixture, never the Foundry domain.
				parsed.ProjectEndpoint = fixture.action.resolvedRemoteContext.projectEndpoint
				t.Setenv("FOUNDRY_PROJECT_ENDPOINT", parsed.ProjectEndpoint+"/wrong-default-project")
				flags.name, flags.protocol = parsed.AgentName, string(parsed.Protocol)
				action := &InvokeAction{
					flags: flags, endpoint: parsed, credential: responseTestCredential{}, noPrompt: true,
				}
				// No cached remoteContext: Run must resolve the endpoint and attach to the mock daemon.
				output, err := captureStdout(t, func() error { return action.Run(t.Context()) })

				assert.Nil(t, action.resolvedRemoteContext)
				fixture.assertIsolated(t, 0)
				if tt.mismatch {
					requireVersionOverrideRoutingFailure(t, err, "does not match requested version")
					assert.NotContains(t, output, "wrong-default-project")
					assert.NotContains(t, output, "Client elapsed:")
					if tt.format == outputRaw {
						assert.True(t, strings.HasSuffix(output, "\r\n\r\n"+reply.body))
						if protocol == "invocations" {
							assert.Contains(t, output, "X-Agent-Invocation-Id: inv_override\r\n")
						}
						assert.NotContains(t, output, "Version override:")
						assert.NotContains(t, output, "azd ai agent invocations")
					} else {
						if protocol == "responses" {
							assert.Contains(t, output, "Response:     resp_override")
							assert.Contains(t, output, "Next:\n  "+fmt.Sprintf(
								`azd ai agent invocations follow --id "resp_override" --agent-endpoint %q`,
								parsed.ProjectEndpoint+fixture.postPath+"?api-version="+apiVersion))
						} else {
							assert.Contains(t, output, "Invocation:   inv_override")
							assert.NotContains(t, output, "Next:")
						}
						assert.Contains(t, output, "Version override: 4; resolved: 3")
						assert.NotContains(t, output, "override-result")
					}
				} else {
					require.NoError(t, err)
					assert.Contains(t, output, "override-result")
					assert.Contains(t, output, "Version override: 4; resolved: 4")
				}
			})
		}
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
				mismatch     bool
			}{
				{name: "selected hosted", wantPickers: 1},
				{name: "ambiguous no-prompt", noPrompt: true},
				{name: "explicit service", agentName: "z-hosted"},
				{name: "deployed name", agentName: "agent"},
				{name: "selected prompt", selectPrompt: true, wantPickers: 1},
				{name: "selected hosted mismatch", wantPickers: 1, mismatch: true},
			} {
				t.Run(fmt.Sprintf("%s/automatic=%t/%s", protocol, automatic, tt.name), func(t *testing.T) {
					fixtureFlags := &invokeFlags{
						message: versionOverrideSource, protocol: protocol, versionOverride: "4", outputFmt: outputDefault,
					}
					body := `{"result":"override-result"}`
					if protocol == "responses" {
						body = versionOverrideStream
					}
					status, resolved := http.StatusOK, "4"
					if tt.mismatch {
						resolved = "3"
						fixtureFlags.userIdentity = "selected-hosted-user"
						if protocol == "responses" {
							fixtureFlags.longRunning = true
						} else {
							status = http.StatusAccepted
							body = `{"invocation_id":"inv_override","status":"accepted"}`
						}
					}
					fixture := newVersionOverrideHTTPFixture(t, fixtureFlags, versionOverrideHTTPReply{
						status: status, resolved: resolved, body: body,
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
					if tt.mismatch {
						requireVersionOverrideRoutingFailure(t, err, "does not match requested version")
						if protocol == "responses" {
							assert.Contains(t, output, "Response:     resp_override")
							assert.Contains(t, output, "Next:\n  "+
								`azd ai agent invocations follow --id "resp_override"`+
								` --protocol responses --agent-name "z-hosted"`)
						} else {
							assert.Contains(t, output, "Invocation:   inv_override")
							assert.NotContains(t, output, "Next:")
						}
						assert.NotContains(t, output, otherProject)
						assert.NotContains(t, output, "unexpected-project")
						assert.NotContains(t, output, "override-result")
					} else {
						require.NoError(t, err)
						assert.Contains(t, output, "override-result")
						assert.Contains(t, output, "Version override: 4; resolved: 4")
					}
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
	status                 int
	resolved               string
	fallback               string
	body                   string
	contentType            string
	conversationStatus     int
	conversationBody       string
	omitInvocationIDHeader bool
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
			if flags.protocol == "invocations" && !initial.omitInvocationIDHeader {
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
		} else if reply.contentType != "" {
			w.Header().Set("Content-Type", reply.contentType)
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
		assert.Equal(t, f.action.resolvedRemoteContext.apiVersion, request.query.Get("api-version"))
		assert.NotContains(t, request.query, "agent_session_id", "never reuse a stored session")
		assert.Equal(t, "Bearer test-token", request.header.Get("Authorization"))
		if f.action.flags.userIdentity != "" {
			assert.Equal(t, []string{f.action.flags.userIdentity}, request.header.Values("x-ms-user-identity"))
		}
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

func requireVersionOverrideRoutingFailure(t *testing.T, err error, reason string) {
	t.Helper()
	require.ErrorContains(t, err, reason)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "the action must return a structured failure, including in raw mode: %v", err)
	assert.Equal(t, exterrors.CodeAgentVersionRoutingFailed, localErr.Code)
	assert.Equal(t, azdext.LocalErrorCategoryCompatibility, localErr.Category)
	assert.Contains(t, localErr.Message, "was not honored:")
	assert.Contains(t, localErr.Message, reason)
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
