// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
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
						requireVersionOverrideNoRecovery(t, err)
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
			name       string
			resolved   string
			fallback   string
			body       string
			omitHeader bool
			wantID     string
			wantErr    string
		}{
			{name: "matched", resolved: "4"},
			{
				name: "mismatch", resolved: "3", wantID: "inv_override",
				wantErr: "does not match requested version",
			},
			{
				name: "missing", wantID: "inv_override",
				wantErr: "expected exactly one x-agent-version-resolved header",
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
				if tt.wantErr != "" {
					localErr := requireVersionOverrideVerificationFailure(t, err, tt.wantErr)
					if tt.wantID != "" {
						requireVersionOverrideRecovery(t, err, "invocations", tt.wantID,
							fixture.action.resolvedRemoteContext.projectEndpoint+fixture.postPath+"?api-version=v1", "")
					} else {
						requireVersionOverrideNoRecovery(t, err)
						assert.Contains(t, localErr.Suggestion, "Could not recover the service-assigned ID:")
						assert.NotContains(t, localErr.Suggestion, "do-not-copy-this-payload")
					}
					assert.NotContains(t, err.Error(), "inv_conflicting")
					assert.NotContains(t, output, "Polling for result")
					assert.NotContains(t, output, "override-result")
					assert.NotContains(t, output, "Client elapsed:")
					if format == outputDefault {
						assert.NotContains(t, output, "Invocation:")
						assert.NotContains(t, output, "inv_override")
						assert.NotContains(t, output, "inv_body_only")
						assert.NotContains(t, output, "inv_conflicting")
					} else {
						assert.True(t, strings.HasSuffix(output, "\r\n\r\n"+accepted), "restore the accepted body verbatim")
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
					assert.NotContains(t, output, "Unverified Invocation ID:")
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
					requireVersionOverrideRecovery(t, err, "responses", "resp_override",
						fixture.action.resolvedRemoteContext.projectEndpoint+fixture.postPath+"?api-version=v1", "")
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

func TestInvokeVersionOverrideLiveBackgroundResponsesIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, tt := range []struct {
		name   string
		format string
		noWait bool
		noID   bool
	}{
		{name: "wait/default", format: outputDefault},
		{name: "no-wait/default", format: outputDefault, noWait: true},
		{name: "wait/raw", format: outputRaw},
		{name: "no-wait/raw", format: outputRaw, noWait: true},
		{name: "deadline/default", format: outputDefault, noID: true},
		{name: "deadline/raw", format: outputRaw, noID: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := versionOverrideCreated
			timeout := 2 * time.Second
			if tt.noID {
				body = ": waiting for identity\n\n"
				timeout = 500 * time.Millisecond
			}
			disconnected := make(chan error, 1)
			fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
				message: versionOverrideSource, protocol: "responses", versionOverride: "4", outputFmt: tt.format,
				longRunning: true, noWait: tt.noWait,
			}, versionOverrideHTTPReply{
				status: http.StatusOK, resolved: "3", body: body,
				afterBody: func(w http.ResponseWriter, r *http.Request) {
					if err := http.NewResponseController(w).Flush(); err != nil {
						disconnected <- err
						return
					}
					// Never send output or completion. Only client cancellation releases the handler.
					<-r.Context().Done()
					disconnected <- nil
				},
			}, nil)
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			output, err := fixture.invokeContext(t, ctx)

			localErr := requireVersionOverrideVerificationFailure(t, err, "does not match requested version")
			fixture.assertIsolated(t, 0)
			if tt.noID {
				requireVersionOverrideNoRecovery(t, err)
				assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
				assert.Contains(t, localErr.Suggestion, "ID recovery interrupted: context deadline exceeded")
				assert.NotErrorIs(t, err, context.DeadlineExceeded, "preserve the verification error, not the read error")
			} else {
				assert.NoError(t, ctx.Err(), "return on response.created, without waiting for completion or a deadline")
				requireVersionOverrideRecovery(t, err, "responses", "resp_override",
					fixture.action.resolvedRemoteContext.projectEndpoint+fixture.postPath+"?api-version=v1", "")
			}
			// Do not cancel ctx here: the recovery reader itself must have closed the live response.
			disconnectTimer := time.NewTimer(time.Second)
			defer disconnectTimer.Stop()
			select {
			case flushErr := <-disconnected:
				assert.NoError(t, flushErr)
			case <-disconnectTimer.C:
				t.Error("the client did not disconnect after recovering or timing out on the ID")
			}
			assert.NotContains(t, output, "override-result")
			assert.NotContains(t, output, "Response:")
			assert.NotContains(t, output, "Unverified Response ID:")
			assert.NotContains(t, output, "azd ai agent invocations")
			assert.NotContains(t, output, "Version override:")
			assert.NotContains(t, output, "Client elapsed:")
			if tt.format == outputRaw {
				assert.True(t, strings.HasPrefix(output, "HTTP/1.1 200 OK\r\n"))
				assert.True(t, strings.HasSuffix(output, "\r\n\r\n"+body), "raw diagnostics restore only captured bytes")
			} else {
				assert.NotContains(t, output, "resp_override")
				assert.NotContains(t, output, "Next:")
			}
		})
	}
}

func TestInvokeVersionOverrideRecoveryReadFailuresIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	const discardedOutput = "event: response.output_text.delta\ndata: " +
		`{"delta":"do-not-render-unverified-output"}` + "\n\n"
	for _, tt := range []struct {
		name       string
		protocol   string
		body       string
		wantID     string
		wantReason string
	}{
		{name: "output before identity", protocol: "responses", body: discardedOutput + versionOverrideCreated,
			wantID: "resp_override"},
		{name: "missing identity", protocol: "responses", body: discardedOutput,
			wantReason: "the response did not contain a service-assigned ID"},
		{name: "malformed SSE", protocol: "responses",
			body:       "event: response.created\ndata: do-not-copy-this-payload\n\n",
			wantReason: "could not read the background response identity"},
		{name: "Responses byte limit", protocol: "responses",
			body:       strings.Repeat(": heartbeat\n\n", 100000) + versionOverrideCreated,
			wantReason: "ID recovery exceeded 1048576 bytes"},
		{name: "Invocations byte limit", protocol: "invocations",
			body:       strings.Repeat(" ", 1024*1024+1) + `{"invocation_id":"inv_too_late"}`,
			wantReason: "ID recovery exceeded 1048576 bytes"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status := http.StatusOK
			if tt.protocol == "invocations" {
				status = http.StatusAccepted
			}
			fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
				message: versionOverrideSource, protocol: tt.protocol, versionOverride: "4", outputFmt: outputDefault,
				longRunning: tt.protocol == "responses",
			}, versionOverrideHTTPReply{
				status: status, resolved: "3", body: tt.body, omitInvocationIDHeader: true,
			}, nil)
			output, err := fixture.invoke(t)

			localErr := requireVersionOverrideVerificationFailure(t, err, "does not match requested version")
			fixture.assertIsolated(t, 0)
			if tt.wantID != "" {
				requireVersionOverrideRecovery(t, err, tt.protocol, tt.wantID,
					fixture.action.resolvedRemoteContext.projectEndpoint+fixture.postPath+"?api-version=v1", "")
			} else {
				requireVersionOverrideNoRecovery(t, err)
				assert.Contains(t, localErr.Suggestion, "Could not recover the service-assigned ID: "+tt.wantReason)
			}
			assert.NotContains(t, localErr.Suggestion, "do-not-copy-this-payload")
			assert.NotContains(t, output, "do-not-render-unverified-output")
			assert.NotContains(t, output, "do-not-copy-this-payload")
			assert.NotContains(t, output, "resp_override")
			assert.NotContains(t, output, "inv_too_late")
			assert.NotContains(t, output, "Response:")
			assert.NotContains(t, output, "Invocation:")
			assert.NotContains(t, output, "Client elapsed:")
		})
	}
}

func TestInvokeVersionOverrideRawInvocationRecoveryKeepsHeaderOnDeadline(t *testing.T) {
	const body = `{"status":"accepted"}`
	fixture := newVersionOverrideHTTPFixture(t, &invokeFlags{
		message: versionOverrideSource, protocol: "invocations", versionOverride: "4", outputFmt: outputRaw,
	}, versionOverrideHTTPReply{
		status: http.StatusAccepted, resolved: "3", body: body,
		afterBody: func(w http.ResponseWriter, r *http.Request) {
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
		},
	}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	output, err := fixture.invokeContext(t, ctx)
	localErr := requireVersionOverrideVerificationFailure(t, err, "does not match requested version")
	requireVersionOverrideRecovery(t, err, "invocations", "inv_override",
		fixture.action.resolvedRemoteContext.projectEndpoint+fixture.postPath+"?api-version=v1", "")
	assert.Contains(t, localErr.Suggestion, "Raw response capture is incomplete:")
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, strings.HasSuffix(output, "\r\n\r\n"+body))
	assert.NotContains(t, output, "azd ai agent invocations")
	fixture.assertIsolated(t, 0)
}

func TestInvokeVersionOverrideUnsuccessfulStatusDoesNotRecoverIntegration(t *testing.T) {
	for _, protocol := range []agent_api.AgentProtocol{
		agent_api.AgentProtocolResponses, agent_api.AgentProtocolInvocations,
	} {
		for _, status := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusTemporaryRedirect} {
			for _, format := range []string{outputDefault, outputRaw} {
				t.Run(fmt.Sprintf("%s/%d/%s", protocol, status, format), func(t *testing.T) {
					payload := versionOverrideStream
					if protocol == agent_api.AgentProtocolInvocations {
						payload = `{"invocation_id":"inv_not_accepted"}`
					}
					reader := strings.NewReader(payload)
					body := &trackingReadCloser{Reader: reader}
					resp := &http.Response{StatusCode: status, Header: make(http.Header), Body: body}
					resp.Header.Set("x-agent-invocation-id", "inv_not_accepted")
					action := &InvokeAction{flags: &invokeFlags{
						versionOverride: "4", longRunning: true, outputFmt: format,
					}}
					var output bytes.Buffer
					// A real target context and longRunning=true enable recovery if the status guard regresses.
					err := action.verifyVersionOverrideResponse(t.Context(), resp, &remoteContext{
						name: "selected-agent", projectEndpoint: "http://127.0.0.1:1/project", apiVersion: "v1",
					}, protocol, &output)

					if status >= http.StatusBadRequest {
						require.NoError(t, err, "leave HTTP failures to the original response handler")
					} else {
						requireVersionOverrideVerificationFailure(t, err, "unexpected HTTP status 307")
						requireVersionOverrideNoRecovery(t, err)
					}
					assert.Same(t, body, resp.Body, "recovery must not replace the response body")
					assert.False(t, body.closed, "recovery must not close the response body")
					if status < http.StatusBadRequest && format == outputRaw {
						assert.True(t, strings.HasSuffix(output.String(), "\r\n\r\n"+payload))
					} else {
						assert.Equal(t, len(payload), reader.Len(), "no recovery reads, even in raw mode for HTTP errors")
						assert.Empty(t, output.String())
					}
				})
			}
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
						flags.longRunning = true
						reply.body = versionOverrideCreated
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
					requireVersionOverrideVerificationFailure(t, err, "does not match requested version")
					id := "inv_override"
					if protocol == "responses" {
						id = "resp_override"
					}
					requireVersionOverrideRecovery(t, err, protocol, id,
						parsed.ProjectEndpoint+fixture.postPath+"?api-version="+apiVersion, flags.userIdentity)
					assert.NotContains(t, err.Error(), "wrong-default-project")
					assert.NotContains(t, output, "Unverified ")
					assert.NotContains(t, output, "azd ai agent invocations")
					assert.NotContains(t, output, "Client elapsed:")
					assert.NotContains(t, output, "Version override:")
					if tt.format == outputRaw {
						assert.True(t, strings.HasSuffix(output, "\r\n\r\n"+reply.body))
					} else {
						assert.NotContains(t, output, id)
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
						localErr := requireVersionOverrideVerificationFailure(t, err, "does not match requested version")
						id := "inv_override"
						if protocol == "responses" {
							id = "resp_override"
						}
						requireVersionOverrideRecovery(t, err, protocol, id, endpoint, flags.userIdentity)
						assert.NotContains(t, localErr.Suggestion, otherProject)
						assert.NotContains(t, localErr.Suggestion, "unexpected-project")
						assert.NotContains(t, localErr.Suggestion, "z-hosted")
						assert.NotContains(t, output, id)
						assert.NotContains(t, output, "override-result")
						assert.NotContains(t, output, "Response:")
						assert.NotContains(t, output, "Invocation:")
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
	conversationStatus     int
	conversationBody       string
	omitInvocationIDHeader bool
	afterBody              func(http.ResponseWriter, *http.Request)
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
		if reply.afterBody != nil {
			reply.afterBody(w, r)
		}
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
	return f.invokeContext(t, t.Context())
}

func (f *versionOverrideHTTPFixture) invokeContext(t *testing.T, ctx context.Context) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		if f.action.flags.protocol == "responses" {
			return f.action.responsesRemote(ctx)
		}
		return f.action.invocationsRemote(ctx)
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

func requireVersionOverrideVerificationFailure(t *testing.T, err error, reason string) *azdext.LocalError {
	t.Helper()
	require.ErrorContains(t, err, reason)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "the action must return a structured failure, including in raw mode: %v", err)
	assert.Equal(t, exterrors.CodeAgentVersionVerificationFailed, localErr.Code)
	assert.Equal(t, azdext.LocalErrorCategoryCompatibility, localErr.Category)
	assert.Contains(t, localErr.Message, "could not be verified:")
	assert.Contains(t, localErr.Message, reason)
	assert.Contains(t, localErr.Suggestion, "may already have executed")
	return localErr
}

func requireVersionOverrideRecovery(t *testing.T, err error, protocol, id, endpoint, userIdentity string) {
	t.Helper()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "recovery must be part of the original structured error: %v", err)
	label, operation := "Invocation", "show"
	if protocol == "responses" {
		label, operation = "Response", "follow"
	}
	assert.Contains(t, localErr.Message, "could not be verified:")
	assert.Contains(t, localErr.Message, "\nUnverified "+label+" ID: "+id)
	assert.NotContains(t, localErr.Suggestion, "Could not recover")
	assert.Contains(t, localErr.Suggestion, "current selection is unchanged")
	var wantCommands []string
	for _, op := range []string{operation, "cancel"} {
		command := fmt.Sprintf("azd ai agent invocations %s --id %q --agent-endpoint %q", op, id, endpoint)
		if userIdentity != "" {
			command += fmt.Sprintf(" --user-identity %q", userIdentity)
		}
		wantCommands = append(wantCommands, command)
	}
	var gotCommands []string
	for line := range strings.SplitSeq(localErr.Suggestion, "\n") {
		if command := strings.TrimSpace(line); strings.HasPrefix(command, "azd ai agent invocations ") {
			gotCommands = append(gotCommands, command)
		}
	}
	assert.Equal(t, wantCommands, gotCommands, "recovery must bind to the exact resolved endpoint and API version")
	assert.NotContains(t, localErr.Suggestion, "--current")
	assert.NotContains(t, localErr.Suggestion, "--version-override")
}

func requireVersionOverrideNoRecovery(t *testing.T, err error) {
	t.Helper()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected the original verification failure: %v", err)
	assert.NotContains(t, localErr.Message, "Unverified Response ID:")
	assert.NotContains(t, localErr.Message, "Unverified Invocation ID:")
	assert.NotContains(t, localErr.Suggestion, "azd ai agent invocations")
	assert.NotContains(t, localErr.Suggestion, "--id")
	assert.NotContains(t, localErr.Suggestion, "--current")
	assert.NotContains(t, localErr.Suggestion, "--version-override")
}

func requireVersionOverrideRouteConflict(t *testing.T, err error) {
	t.Helper()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected a structured argument conflict, got %v", err)
	assert.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
	assert.Contains(t, localErr.Message, "--version-override requires a remote hosted agent")
	assert.Contains(t, localErr.Suggestion, "local, A2A, or prompt agents")
}
