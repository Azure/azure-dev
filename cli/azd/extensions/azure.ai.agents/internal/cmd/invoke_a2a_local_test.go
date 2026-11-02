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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalA2ARejectsRemovedPathFlag(t *testing.T) {
	command := newInvokeCommand(nil)
	require.ErrorContains(t, command.ParseFlags([]string{"--a2a-path=/rpc"}), "unknown flag: --a2a-path")
}

func TestLocalA2AHelpRestrictions(t *testing.T) {
	command := newInvokeCommand(nil)
	_, localHelp, found := strings.Cut(command.Long, "Local Development:")
	require.True(t, found)
	localHelp, _, found = strings.Cut(localHelp, "Sessions & Identity:")
	require.True(t, found)
	for _, flag := range append(slices.Clone(localA2AUnsupportedFlags), "client-header") {
		require.Contains(t, localHelp, "--"+flag)
	}
	require.Contains(t, command.Flags().Lookup("user-identity").Usage, "not supported with local a2a")
	require.Contains(t, command.Flags().Lookup("call-id").Usage, "local responses/invocations only")
}

func TestLocalA2AFlagValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		flags invokeFlags
		want  string
	}{
		{"default", nil, invokeFlags{local: true, protocol: "a2a", port: 8080}, ""},
		{"minimum port", nil, invokeFlags{local: true, protocol: "a2a", port: 1}, ""},
		{"maximum port", nil, invokeFlags{local: true, protocol: "a2a", port: 65535}, ""},
		{"zero port", nil, invokeFlags{local: true, protocol: "a2a"}, "--port"},
		{"negative port", nil, invokeFlags{local: true, port: -1}, "--port"},
		{"large port", nil, invokeFlags{local: true, port: 65536}, "--port"},
		{"client header", nil, invokeFlags{local: true, protocol: "a2a", port: 8080,
			clientHeaders: []string{"x-client-test: v"}}, "--client-header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newInvokeCommand(nil)
			require.NoError(t, command.ParseFlags(tc.args))
			tc.flags.message = "hi"
			err := validateInvokeArgs(command, &tc.flags, true)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
	// Explicit empty values and explicitly false reset switches must not turn
	// into silent no-ops, including when A2A is selected after parsing.
	for _, flag := range localA2AUnsupportedFlags {
		for _, value := range []string{"", "value"} {
			if flag == "new-session" || flag == "new-conversation" {
				value = map[bool]string{true: "true", false: "false"}[value != ""]
			}
			t.Run(flag+"="+value, func(t *testing.T) {
				command := newInvokeCommand(nil)
				require.NoError(t, command.ParseFlags([]string{"--" + flag + "=" + value}))
				flags := &invokeFlags{local: true, port: 8080, protocol: "a2a", message: "hi"}
				require.ErrorContains(t, validateInvokeArgs(command, flags, true), "--"+flag+" is not supported")
				flags.protocol = ""
				captureLocalA2AExplicitFlags(command, flags)
				require.NoError(t, validateLocalA2AOptions(flags, ""))
				require.ErrorContains(t, validateLocalA2AOptions(flags, agent_api.AgentProtocolA2A), "--"+flag)
			})
		}
	}
}

func TestLocalA2AInvalidFlagsBeforeHostAccess(t *testing.T) {
	for _, args := range [][]string{
		{"--port=0"}, {"--port=65536"},
		{"--session-id="}, {"--new-session=false"}, {"--conversation-id=x"}, {"--new-conversation"},
		{"--version=1"}, {"--user-identity=u"}, {"--call-id=c"}, {"--client-header=x-client-test: v"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			clearPreviewProcess(t)
			t.Chdir(t.TempDir())
			_, env := startPreviewCommandServer(t, nil, nil, nil)
			t.Setenv(featureflags.A2A.EnvVar, "false")
			command := newInvokeCommand(nil)
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(append([]string{"--local", "-p", "a2a", "hi"}, args...))
			err := command.ExecuteContext(t.Context())
			require.Error(t, err)
			require.Zero(t, env.reads.Load(), "invalid local flags should not reach azd host or auth")
		})
	}
}

func localA2ATestResponse(request *http.Request, code int, body string) *http.Response {
	return &http.Response{Request: request, StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Proto: "HTTP/1.1", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestLocalA2ASelectedVersionAndProtocol(t *testing.T) {
	for _, tc := range []struct{ preview, explicit, wire string }{
		{"false", "", "0.3"}, {"true", "", "1.0"}, {"true", "0.3", "0.3"}, {"true", "1.0", "1.0"},
	} {
		for _, autodetect := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/autodetect=%t", tc.preview, tc.explicit, autodetect), func(t *testing.T) {
				clearPreviewProcess(t)
				t.Setenv(featureflags.A2A.EnvVar, tc.preview)
				project := &azdext.ProjectConfig{Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"agent": {Name: "agent", Host: AiAgentHost, AdditionalProperties: mustStruct(t, map[string]any{
						"kind": "hosted", "name": "agent", "protocols": []any{map[string]any{"protocol": "a2a", "version": "1.0"}},
					})},
				}}
				_, env := startPreviewCommandServer(t, nil, project, nil)
				var output bytes.Buffer
				flags := &invokeFlags{local: true, protocol: "a2a", port: 8181, message: "hi", a2aVersion: tc.explicit}
				if autodetect {
					flags.protocol = ""
				}
				sends := 0
				action := &InvokeAction{flags: flags, noPrompt: true, a2aWriter: &output, a2aErrorWriter: io.Discard,
					credential: invokeUsageFailingCredential{},
					a2aTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						sends++
						require.Equal(t, "http://localhost:8181/a2a", r.URL.String())
						require.Equal(t, http.MethodPost, r.Method)
						require.Empty(t, r.URL.RawQuery)
						require.Equal(t, http.Header{"Content-Type": {"application/json"}, "A2a-Version": {tc.wire}}, r.Header)
						var request struct {
							ID     string `json:"id"`
							Params struct {
								Message struct {
									MessageID string `json:"messageId"`
								} `json:"message"`
							} `json:"params"`
						}
						body, err := io.ReadAll(r.Body)
						require.NoError(t, err)
						require.NoError(t, json.Unmarshal(body, &request))
						build, err := a2aRequestBuilderForVersion(tc.wire)
						require.NoError(t, err)
						want, err := json.Marshal(build("hi", request.ID, request.Params.Message.MessageID))
						require.NoError(t, err)
						require.Equal(t, want, body)
						if tc.wire == "1.0" {
							return localA2ATestResponse(r, 200, `{"result":{"message":{"parts":[{"text":"version-ok"}]}}}`), nil
						}
						return localA2ATestResponse(r, 200, `{"result":{"parts":[{"kind":"text","text":"version-ok"}]}}`), nil
					})}
				require.NoError(t, action.Run(t.Context()))
				require.Equal(t, 1, sends)
				require.Contains(t, output.String(), "[local] version-ok")
				require.NotContains(t, output.String(), "Session:")
				require.NotContains(t, output.String(), "Conversation:")
				env.mu.Lock()
				require.Empty(t, env.selected, "local invoke must not resolve the cloud environment")
				env.mu.Unlock()
			})
		}
	}
}

func TestLocalA2AValidationAfterAutodetect(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, explicitFlag, want string
		flags                              invokeFlags
	}{
		{"session", "a2a", "--session-id=", "--session-id", invokeFlags{}},
		{"reset", "a2a", "--new-session=false", "--new-session", invokeFlags{}},
		{"conversation", "a2a", "--conversation-id=", "--conversation-id", invokeFlags{}},
		{"conversation reset", "a2a", "--new-conversation=false", "--new-conversation", invokeFlags{}},
		{"identity", "a2a", "--user-identity=", "--user-identity", invokeFlags{}},
		{"call id", "a2a", "--call-id=", "--call-id", invokeFlags{}},
		{"headers", "a2a", "", "--client-header", invokeFlags{clientHeaders: []string{"x-client-test: v"}}},
		{"file version", "a2a", "", "requires an explicit --a2a-version", invokeFlags{inputFile: "request.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearPreviewProcess(t)
			t.Setenv(featureflags.A2A.EnvVar, "true")
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile("request.json", []byte(`{}`), 0600))
			_, env := startPreviewCommandServer(t, nil, &azdext.ProjectConfig{
				Path: root, Services: map[string]*azdext.ServiceConfig{"agent": {
					Name: "agent", Host: AiAgentHost, AdditionalProperties: mustStruct(t, map[string]any{
						"kind": "hosted", "name": "agent",
						"protocols": []any{map[string]any{"protocol": tc.protocol, "version": "1.0"}},
					}),
				}},
			}, nil)
			flags := tc.flags
			flags.local, flags.port, flags.message = true, 8080, "hi"
			if tc.explicitFlag != "" {
				command := newInvokeCommand(nil)
				require.NoError(t, command.ParseFlags([]string{tc.explicitFlag}))
				captureLocalA2AExplicitFlags(command, &flags)
			}
			sends := 0
			a := &InvokeAction{flags: &flags, noPrompt: true, a2aWriter: io.Discard,
				credential: invokeUsageFailingCredential{},
				a2aTransport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					sends++
					return nil, errors.New("unexpected request")
				})}
			require.ErrorContains(t, a.Run(t.Context()), tc.want)
			require.Zero(t, sends)
			env.mu.Lock()
			require.Empty(t, env.selected)
			env.mu.Unlock()
		})
	}
}

func TestLocalA2AFileBytes(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"0.3", "1.0"} {
		t.Run(version, func(t *testing.T) {
			content := []byte(" \r\nopaque JSON-RPC\x00\xff\n")
			file := filepath.Join(t.TempDir(), "request.bin")
			require.NoError(t, os.WriteFile(file, content, 0600))
			a := &InvokeAction{flags: &invokeFlags{local: true, port: 12345, inputFile: file},
				a2aWriter: io.Discard, a2aErrorWriter: io.Discard,
				a2aTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					require.Equal(t, content, body)
					require.Equal(t, "http://localhost:12345/a2a", r.URL.String())
					require.Equal(t, version, r.Header.Get(a2aVersionHeader))
					return localA2ATestResponse(r, 200, `{}`), nil
				})}
			require.NoError(t, a.a2aLocal(t.Context(), version))
		})
	}
}

func TestLocalA2AResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, contentType, want, wantError string
		status                                   int
		raw                                      bool
	}{
		{"completed task", `{"result":{"task":{"status":{"state":"TASK_STATE_COMPLETED",` +
			`"message":{"parts":[{"text":"Done"}]}},"artifacts":[{"parts":[{"text":"answer"}]}]}}}`,
			"application/json", "[local] answer", "", 200, false},
		{"non-json", "plain reply", "text/plain", "[local] plain reply", "", 200, false},
		{"json fallback", `{"unexpected":42}`, "application/json", `"unexpected": 42`, "", 200, false},
		{"rpc error", `{"error":{"code":-32601,"message":"wrong method"}}`,
			"application/json", "", "agent error (-32601)", 200, false},
		{"http error", "endpoint missing", "text/plain", "", "HTTP 404", 404, false},
		{"http failure", "server failed", "text/plain", "", "server failed", 500, false},
		{"SSE", "event: message\r\ndata: {\"text\":\"chunk\"}\r\n\r\n", "text/event-stream", "", "", 200, true},
		{"raw JSON", " \r\n{\"unmodified\": true}\n", "application/json", "", "", 200, true},
		{"raw http error", "bad\x00\xff", "text/plain", "", "HTTP 400", 400, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			sends := 0
			a := &InvokeAction{flags: &invokeFlags{local: true, port: 8080, message: "hi"}, a2aWriter: &output,
				a2aTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					sends++
					if tc.raw {
						require.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
					}
					resp := localA2ATestResponse(r, tc.status, tc.body)
					resp.Header.Set("Content-Type", tc.contentType)
					return resp, nil
				})}
			if tc.raw {
				a.flags.outputFmt = outputRaw
			}
			err := a.a2aLocal(t.Context(), "1.0")
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 1, sends, "no route fallback or polling even after an HTTP/RPC error")
			if tc.raw {
				want := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: %s\r\n\r\n%s",
					tc.status, http.StatusText(tc.status), tc.contentType, tc.body)
				require.Equal(t, want, output.String())
			} else if tc.want != "" {
				require.Contains(t, output.String(), tc.want)
			}
		})
	}
}

func TestLocalA2ARetryAndErrorChains(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		failure   error
		wantCalls int
		refused   bool
	}{
		{"refused then success", nil, 2, true},
		{"ambiguous EOF", io.ErrUnexpectedEOF, 1, false},
		{"reset", syscall.ECONNRESET, 1, false},
		{"deadline", context.DeadlineExceeded, 1, false},
		{"canceled", context.Canceled, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var firstBody []byte
			a := &InvokeAction{flags: &invokeFlags{local: true, port: 8080, message: "hi"},
				a2aWriter: io.Discard, a2aErrorWriter: io.Discard,
				a2aTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					if calls == 1 {
						firstBody = body
					} else {
						require.Equal(t, firstBody, body, "retry must reuse request IDs")
					}
					if tc.refused && calls == 1 {
						return nil, fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
					}
					if tc.failure != nil {
						return nil, tc.failure
					}
					return localA2ATestResponse(r, 200, `{}`), nil
				})}
			err := a.a2aLocal(t.Context(), "1.0")
			if tc.failure == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.failure)
				require.NotContains(t, err.Error(), "is the agent running?", "transport failures need their actual cause")
			}
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestLocalA2ACancelAndTimeout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		timeout int
		refused bool
	}{
		{"cancel during retry", 0, true}, {"timeout during retry", 1, true},
		{"cancel during request", 0, false}, {"timeout during request", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			a := &InvokeAction{flags: &invokeFlags{local: true, port: 8080, message: "hi", timeout: tc.timeout},
				a2aWriter: io.Discard, a2aErrorWriter: io.Discard,
				a2aTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if tc.timeout == 0 {
						cancel()
					}
					if tc.refused {
						return nil, syscall.ECONNREFUSED
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				})}
			start := time.Now()
			err := a.a2aLocal(ctx, "1.0")
			if tc.timeout == 0 {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Less(t, time.Since(start), 3*time.Second, "timeout must bound all retry attempts")
			}
			require.NotContains(t, err.Error(), "is the agent running?")
			require.Positive(t, calls)
		})
	}
}

func TestLocalA2ANeverFollowsRedirects(t *testing.T) {
	t.Parallel()
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, location := range []string{"/other", "http://example.com/execute"} {
			t.Run(fmt.Sprintf("%d/%s", code, location), func(t *testing.T) {
				calls := 0
				a := &InvokeAction{flags: &invokeFlags{local: true, port: 8080, message: "hi"}, a2aWriter: io.Discard,
					a2aTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						calls++
						resp := localA2ATestResponse(r, code, "redirect")
						resp.Header.Set("Location", location)
						return resp, nil
					})}
				require.ErrorContains(t, a.a2aLocal(t.Context(), "1.0"), "redirects are not followed")
				require.Equal(t, 1, calls)
			})
		}
	}
}

func TestLocalA2AActualLocalhostPort(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "/a2a", r.RequestURI)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "", r.Header.Get("Authorization"))
		assert.Equal(t, "", r.Header.Get(agent_api.AgentUserIDHeader))
		_, _ = io.WriteString(w, `{"result":{"message":{"parts":[{"text":"actual-localhost"}]}}}`)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	var output bytes.Buffer
	a := &InvokeAction{flags: &invokeFlags{local: true, port: port, message: "hi", timeout: 5},
		a2aWriter: &output}
	require.NoError(t, a.a2aLocal(t.Context(), "1.0"))
	require.Equal(t, 1, calls)
	require.Contains(t, output.String(), "[local] actual-localhost")
}

// localA2AContextReader models a stream that stops only when its request's
// context ends, so timeout/cancellation tests cover response-body consumption.
type localA2AContextReader struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (r localA2AContextReader) Read([]byte) (int, error) {
	if r.cancel != nil {
		r.cancel()
	}
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func TestLocalA2AStreamingCancellation(t *testing.T) {
	t.Parallel()
	for _, raw := range []bool{false, true} {
		for _, timeout := range []int{0, 1} {
			t.Run(fmt.Sprintf("raw=%t/timeout=%d", raw, timeout), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var output bytes.Buffer
				sends := 0
				a := &InvokeAction{flags: &invokeFlags{local: true, port: 8080, message: "hi", timeout: timeout},
					a2aWriter: &output, a2aTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						sends++
						resp := localA2ATestResponse(r, 200, "")
						reader := localA2AContextReader{ctx: r.Context()}
						if timeout == 0 {
							reader.cancel = cancel
						}
						resp.Header.Set("Content-Type", "text/event-stream")
						resp.Body = io.NopCloser(io.MultiReader(strings.NewReader("data: first\n\n"), reader))
						return resp, nil
					})}
				if raw {
					a.flags.outputFmt = outputRaw
				}
				err := a.a2aLocal(ctx, "1.0")
				if timeout == 0 {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				}
				require.Equal(t, 1, sends, "a partially consumed stream must not be replayed")
				if raw {
					require.Contains(t, output.String(), "data: first\n\n")
				}
			})
		}
	}
}

type localA2AFailingReader struct{ err error }

func (r localA2AFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestLocalA2AReadAndWriteErrors(t *testing.T) {
	t.Parallel()
	failure := errors.New("test I/O failure")
	for _, raw := range []bool{false, true} {
		t.Run(strconv.FormatBool(raw), func(t *testing.T) {
			a := &InvokeAction{flags: &invokeFlags{local: true, port: 8080, message: "hi"}, a2aWriter: io.Discard,
				a2aTransport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					resp := localA2ATestResponse(r, 200, "")
					resp.Body = io.NopCloser(localA2AFailingReader{failure})
					return resp, nil
				})}
			if raw {
				a.flags.outputFmt = outputRaw
			}
			require.ErrorIs(t, a.a2aLocal(t.Context(), "1.0"), failure)
		})
	}
	resp := localA2ATestResponse(nil, 200, `{"result":{"message":{"parts":[{"text":"hello"}]}}}`)
	require.ErrorIs(t, handleA2AResponseTo(localA2AFailingWriter{failure}, resp, "local", false, "1.0"), failure)
}

type localA2AFailingWriter struct{ err error }

func (w localA2AFailingWriter) Write([]byte) (int, error) { return 0, w.err }
