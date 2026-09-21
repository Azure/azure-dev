// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
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
	assert.Equal(t,
		"Test a hosted version (or latest) with an isolated invocation; version headers are optional", flag.Usage)
	help := strings.Join(strings.Fields(cmd.Long), " ")
	for _, text := range []string{
		"through the x-agent-version-override header for manual testing of a candidate version",
		"fresh, isolated session and, for Responses, a new conversation",
		"Session/conversation and operation IDs are not saved as the current selection",
		"Version headers are optional: missing or unusable version information produces a warning, not a failure",
		"Inspect the agent's response to confirm the candidate's behavior before increasing traffic",
		"An explicit service-reported fallback or a different concrete version still returns an error",
		"as do HTTP and agent errors",
		"An error does not undo work already executed",
		"Known background operation IDs remain available for explicit follow/show/cancel without changing current state",
		"latest follows the service's floating routing behavior",
		"No override is sent unless requested. Raw output stays unchanged; warnings go to stderr",
	} {
		assert.Contains(t, help, text)
	}
	assert.NotContains(t, strings.ToLower(cmd.Flags().FlagUsages()), "strict")
	assert.NotContains(t, help, "resolved the requested version without fallback")
	assert.NotContains(t, help, "the original verification error is still returned")
	assert.Contains(t, cmd.Example, "Test a candidate version using an isolated invocation")
	assert.NotContains(t, cmd.Long, "without changing its traffic split")
	assert.NotContains(t, cmd.Example, "without changing the endpoint traffic split")
	assert.Contains(t, cmd.Flags().Lookup("version").Usage, "session backed by that version")
}

func TestOptionalAgentVersionHeader(t *testing.T) {
	for _, tt := range []struct {
		name   string
		values []string
		want   string
	}{
		{name: "absent"},
		{name: "empty", values: []string{""}},
		{name: "whitespace", values: []string{" \t"}},
		{name: "bad characters", values: []string{"3,4"}},
		{name: "unsafe", values: []string{"bad\x1b[31m\r\nINJECTED"}},
		{name: "duplicate", values: []string{"3", "3"}},
		{name: "conflicting", values: []string{"3", "4"}},
		{name: "latest", values: []string{"latest"}},
		{name: "latest mixed case", values: []string{" LaTeSt "}},
		{name: "too long", values: []string{strings.Repeat("a", 129)}},
		{name: "trim", values: []string{" 3 \t"}, want: "3"},
		{name: "safe", values: []string{"Release_3.1-beta"}, want: "Release_3.1-beta"},
	} {
		for _, name := range []string{agentVersionResolvedHeader, agentVersionResolutionHeader} {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				headers := make(http.Header)
				for _, value := range tt.values {
					headers.Add(name, value)
				}
				assert.Equal(t, tt.want, optionalAgentVersionHeader(headers, name))
			})
		}
	}
}

func TestReportVersionOverrideResponse(t *testing.T) {
	const fallbackReason = "the service reported a version fallback"
	const unsafe = "bad\x1b[31m\r\nINJECTED"
	config := newInvokeUserConfigServer()
	rc := &remoteContext{agentKey: "ordinary", azdClient: newInvokeTestAzdClient(t, config)}
	for _, tt := range []struct {
		name           string
		requested      string
		resolved       []string
		fallback       []string
		resolution     []string
		wantResolved   string
		wantResolution string
		warning        bool
		reason         string
	}{
		{name: "metadata absent", warning: true},
		{name: "resolved empty", resolved: []string{""}, warning: true},
		{name: "resolved whitespace", resolved: []string{" \t"}, warning: true},
		{name: "resolved bad characters", resolved: []string{"4,5"}, warning: true},
		{name: "resolved unsafe", resolved: []string{unsafe}, warning: true},
		{name: "resolved duplicate", resolved: []string{"4", "4"}, warning: true},
		{name: "resolved conflicting", resolved: []string{"4", "5"}, warning: true},
		{name: "resolved latest", resolved: []string{"latest"}, warning: true},
		{name: "fallback absent", resolved: []string{"4"}, wantResolved: "4"},
		{name: "fallback false", resolved: []string{"4"}, fallback: []string{"false"}, wantResolved: "4"},
		{name: "trim", resolved: []string{" 4 "}, fallback: []string{" FALSE "}, wantResolved: "4"},
		{name: "latest resolves to 1", requested: "latest", resolved: []string{"1"}, wantResolved: "1"},
		{
			name: "fallback empty", resolved: []string{"4"}, fallback: []string{""},
			wantResolved: "4", warning: true,
		},
		{
			name: "fallback bogus", resolved: []string{"4"}, fallback: []string{"bogus"},
			wantResolved: "4", warning: true,
		},
		{
			name: "fallback unsafe", resolved: []string{"4"}, fallback: []string{unsafe},
			wantResolved: "4", warning: true,
		},
		{
			name: "fallback multiple false", resolved: []string{"4"}, fallback: []string{"false", "false"},
			wantResolved: "4", warning: true,
		},
		{
			name: "resolution reported", resolved: []string{"4"}, resolution: []string{" flightoverride "},
			wantResolved: "4", wantResolution: "flightoverride",
		},
		{name: "resolution empty", resolved: []string{"4"}, resolution: []string{""}, wantResolved: "4"},
		{name: "resolution unsafe", resolved: []string{"4"}, resolution: []string{unsafe}, wantResolved: "4"},
		{name: "resolution latest", resolved: []string{"4"}, resolution: []string{"latest"}, wantResolved: "4"},
		{
			name: "resolution duplicate", resolved: []string{"4"}, resolution: []string{"override", "default"},
			wantResolved: "4",
		},
		{
			name: "fallback true", resolved: []string{"4"}, fallback: []string{" true "},
			wantResolved: "4", reason: fallbackReason,
		},
		{name: "fallback without resolved", fallback: []string{"true"}, warning: true, reason: fallbackReason},
		{
			name: "fallback false true", resolved: []string{"4"}, fallback: []string{"false", "TRUE"},
			wantResolved: "4", warning: true, reason: fallbackReason,
		},
		{
			name: "fallback true false", resolved: []string{"4"}, fallback: []string{"true", "false"},
			wantResolved: "4", warning: true, reason: fallbackReason,
		},
		{
			name: "fallback true true", resolved: []string{"4"}, fallback: []string{"true", "true"},
			wantResolved: "4", warning: true, reason: fallbackReason,
		},
		{
			name: "latest fallback", requested: "latest", resolved: []string{"1"}, fallback: []string{"true"},
			wantResolved: "1", reason: fallbackReason,
		},
		{
			name: "mismatch", resolved: []string{"1"}, wantResolved: "1",
			reason: `resolved version "1" does not match requested version "4"`,
		},
		{
			name: "fallback precedes mismatch", resolved: []string{"1"}, fallback: []string{"true"},
			wantResolved: "1", reason: fallbackReason,
		},
	} {
		for _, format := range []string{outputDefault, outputRaw} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				requested := tt.requested
				if requested == "" {
					requested = "4"
				}
				headers := make(http.Header)
				for name, values := range map[string][]string{
					agentVersionResolvedHeader: tt.resolved, agentVersionFallbackHeader: tt.fallback,
					agentVersionResolutionHeader: tt.resolution,
				} {
					for _, value := range values {
						headers.Add(name, value)
					}
				}
				const payload = `{"id":"operation-not-to-save"}`
				reader := strings.NewReader(payload)
				body := &trackingReadCloser{Reader: reader}
				resp := &http.Response{StatusCode: http.StatusAccepted, Header: headers, Body: body}
				action := &InvokeAction{
					flags: &invokeFlags{versionOverride: requested, outputFmt: format}, resolvedRemoteContext: rc,
				}
				var stdout, stderr bytes.Buffer
				err := action.reportVersionOverrideResponse(resp, &stdout, &stderr)
				if tt.reason == "" {
					require.NoError(t, err)
				} else {
					message := fmt.Sprintf("agent version override %q was not honored: %s", requested, tt.reason)
					require.EqualError(t, err, message)
					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					assert.Equal(t, exterrors.CodeAgentVersionRoutingFailed, localErr.Code)
					assert.Equal(t, azdext.LocalErrorCategoryCompatibility, localErr.Category)
					assert.Equal(t,
						"confirm the candidate behavior manually; the request may already have executed; "+
							"no automatic retries",
						localErr.Suggestion)
				}
				wantWarning := ""
				if tt.warning {
					wantWarning = "Warning: agent version information is unavailable or incomplete; " +
						"confirm the candidate behavior manually.\n"
				}
				assert.Equal(t, wantWarning, stderr.String())
				wantSummary := ""
				if format != outputRaw {
					resolved := tt.wantResolved
					if resolved == "" {
						resolved = "not reported"
					}
					wantSummary = fmt.Sprintf("Version override: %s; resolved: %s", requested, resolved)
					if tt.wantResolution != "" {
						wantSummary += "; resolution: " + tt.wantResolution
					}
					wantSummary += "\n"
				}
				assert.Equal(t, wantSummary, stdout.String(), "raw bytes are emitted by the normal protocol path")
				assert.NotContains(t, stdout.String()+stderr.String(), unsafe)
				assert.Same(t, body, resp.Body)
				assert.Equal(t, len(payload), reader.Len(), "reporting must not read the response body")
				assert.False(t, body.closed)
			})
		}
	}
	config.mu.Lock()
	defer config.mu.Unlock()
	assert.Empty(t, config.values, "reporting must not save session, conversation, or operation IDs")
	assert.Equal(t, "ordinary", rc.agentKey)
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
			var output, warnings bytes.Buffer
			require.NoError(t, action.reportVersionOverrideResponse(resp, &output, &warnings))
			assert.Empty(t, output.String())
			assert.Empty(t, warnings.String())
			assert.Empty(t, resp.Header)
			assert.Equal(t, tt.status, resp.StatusCode)
			assert.Same(t, body, resp.Body)
			assert.False(t, body.closed)
			remaining, err := io.ReadAll(body)
			require.NoError(t, err)
			assert.Equal(t, "original response", string(remaining))
		})
	}
}

func TestReportVersionOverrideResponseWriterErrors(t *testing.T) {
	warningErr, summaryErr := errors.New("warning writer failed"), errors.New("summary writer failed")
	for _, tt := range []struct {
		name        string
		warningFail bool
		summaryFail bool
		raw         bool
		fallback    bool
	}{
		{name: "warning", warningFail: true},
		{name: "summary", summaryFail: true},
		{name: "both", warningFail: true, summaryFail: true},
		{name: "routing and writers", warningFail: true, summaryFail: true, fallback: true},
		{name: "raw warning", warningFail: true, raw: true},
		{name: "raw ignores summary writer", summaryFail: true, raw: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var summary, warnings io.Writer = io.Discard, io.Discard
			if tt.warningFail {
				warnings = versionOverrideFailingWriter{warningErr}
			}
			if tt.summaryFail {
				summary = versionOverrideFailingWriter{summaryErr}
			}
			headers := make(http.Header)
			if !tt.warningFail {
				headers.Set(agentVersionResolvedHeader, "4")
			}
			var messages []string
			if tt.fallback {
				headers.Set(agentVersionFallbackHeader, "true")
				messages = append(messages,
					`agent version override "4" was not honored: the service reported a version fallback`)
			}
			format := outputDefault
			if tt.raw {
				format = outputRaw
			}
			action := &InvokeAction{flags: &invokeFlags{versionOverride: "4", outputFmt: format}}
			err := action.reportVersionOverrideResponse(
				&http.Response{StatusCode: http.StatusOK, Header: headers}, summary, warnings)
			if tt.warningFail {
				assert.ErrorIs(t, err, warningErr)
				messages = append(messages, warningErr.Error())
			}
			if tt.summaryFail && !tt.raw {
				assert.ErrorIs(t, err, summaryErr)
				messages = append(messages, summaryErr.Error())
			}
			if len(messages) == 0 {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, strings.Join(messages, "\n"))
			}
			if tt.fallback {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeAgentVersionRoutingFailed, localErr.Code)
			}
		})
	}
}

type versionOverrideFailingWriter struct{ err error }

func (w versionOverrideFailingWriter) Write(_ []byte) (int, error) {
	return 0, w.err
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

func TestInvokeVersionOverrideRejectsNonSuccessStatus(t *testing.T) {
	for _, code := range []int{100, 101, 103, 199, 300, 301, 302, 303, 304, 307, 308, 399} {
		for _, format := range []string{outputDefault, outputRaw} {
			for _, resolved := range []string{"", "4"} {
				t.Run(fmt.Sprintf("%d/%s/resolved=%s", code, format, resolved), func(t *testing.T) {
					headers := make(http.Header)
					if resolved != "" {
						headers.Set(agentVersionResolvedHeader, resolved)
					}
					body := &trackingReadCloser{Reader: strings.NewReader("original response")}
					resp := &http.Response{StatusCode: code, Header: headers, Body: body}
					action := &InvokeAction{flags: &invokeFlags{versionOverride: "4", outputFmt: format}}
					var output, warnings bytes.Buffer
					err := action.reportVersionOverrideResponse(resp, &output, &warnings)
					require.EqualError(t, err,
						fmt.Sprintf("unexpected HTTP status %d; expected a successful 2xx response", code))
					assert.Empty(t, output.String())
					assert.Empty(t, warnings.String())
					assert.Same(t, body, resp.Body)
					assert.False(t, body.closed)
					remaining, readErr := io.ReadAll(body)
					require.NoError(t, readErr)
					assert.Equal(t, "original response", string(remaining))
				})
			}
		}
	}
}
