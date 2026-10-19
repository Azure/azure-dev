// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAPIProbeProgress(t *testing.T) {
	for _, test := range []struct {
		name        string
		raw         bool
		interactive bool
	}{
		{name: "interactive", interactive: true},
		{name: "redirected"},
		{name: "raw interactive", raw: true, interactive: true},
		{name: "raw redirected", raw: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, printOpenAPIProbeProgress(&out, test.raw, test.interactive))
			if test.interactive && !test.raw {
				assert.Contains(t, out.String(), "Checking optional OpenAPI metadata...")
			} else {
				assert.Empty(t, out.String())
			}
		})
	}
}

func TestInvocationsRemoteMetadataBeforeCompletion(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".azure", "test"), 0o700))
			client := newOpenAPIProbeClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{Path: root}})
			out, err := os.CreateTemp(root, "output")
			require.NoError(t, err)
			previous := os.Stdout
			os.Stdout = out
			t.Cleanup(func() {
				os.Stdout = previous
				require.NoError(t, out.Close())
			})
			probed := false
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					resp := openAPITestResponse(req, 200, io.NopCloser(strings.NewReader(`{"result":"ok"}`)))
					resp.Header.Set("x-agent-session-id", "sid")
					return resp, nil
				}
				probed = true
				data, readErr := os.ReadFile(out.Name())
				require.NoError(t, readErr)
				assert.Contains(t, string(data), `"result": "ok"`)
				assert.NotContains(t, string(data), "Client elapsed:")
				assert.NotContains(t, string(data), "Next:")
				return openAPITestResponse(req, status, io.NopCloser(strings.NewReader("{}"))), nil
			})
			action := &InvokeAction{
				flags:                &invokeFlags{message: "{}", newSession: true},
				credential:           responseTestCredential{},
				invocationsTransport: transport,
				resolvedRemoteContext: &remoteContext{
					projectEndpoint: "https://example.com", name: "agent",
					agentKey: "agent-key", azdClient: client,
				},
			}
			require.NoError(t, action.invocationsRemote(t.Context()))
			assert.True(t, probed)
			data, err := os.ReadFile(out.Name())
			require.NoError(t, err)
			assert.Contains(t, string(data), "Client elapsed:")
			assert.NotContains(t, string(data), "Checking optional OpenAPI metadata...")
		})
	}
}

type openAPIProjectServer struct {
	helpersProjectServer
	onGet func(context.Context) error
}

func (s *openAPIProjectServer) Get(
	ctx context.Context, request *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	if s.onGet != nil {
		if err := s.onGet(ctx); err != nil {
			return nil, err
		}
	}
	return s.helpersProjectServer.Get(ctx, request)
}

func newOpenAPIProbeClient(t *testing.T, project azdext.ProjectServiceServer) *azdext.AzdClient {
	t.Helper()
	address := newInvokeRemoteContextTestAzdServer(t, project, &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
	})
	t.Setenv("AZD_SERVER", address)
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

type openAPIResponseBody struct {
	io.Reader
	onRead     func()
	closed     bool
	closeCalls int
	closeErr   error
}

func (b *openAPIResponseBody) Read(p []byte) (int, error) {
	if b.onRead != nil {
		b.onRead()
	}
	return b.Reader.Read(p)
}

func (b *openAPIResponseBody) Close() error {
	b.closed = true
	b.closeCalls++
	return b.closeErr
}

func TestInvocationResponseBodyCloseOnce(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		closeErr error
	}{
		{name: "success"},
		{name: "cleanup failure", closeErr: errors.New("response cleanup failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			original := &openAPIResponseBody{Reader: strings.NewReader("result"), closeErr: test.closeErr}
			body := &invocationResponseBody{ReadCloser: original}
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			assert.Equal(t, "result", string(data))
			assert.ErrorIs(t, body.Close(), test.closeErr)
			assert.ErrorIs(t, body.Close(), test.closeErr)
			assert.True(t, body.closed)
			assert.Equal(t, 1, original.closeCalls)
		})
	}
}

func openAPITestResponse(request *http.Request, status int, body io.ReadCloser) *http.Response {
	return &http.Response{
		Request: request, StatusCode: status, Status: http.StatusText(status),
		Proto: "HTTP/1.1", Header: make(http.Header), Body: body,
	}
}

func TestOpenAPIProbeCacheAndFailures(t *testing.T) {
	for _, test := range []struct {
		name         string
		status       int
		transportErr error
		readErr      bool
		writeErr     bool
		cacheHit     bool
		wantStage    string
	}{
		{name: "valid spec", status: http.StatusOK},
		{name: "existing cache", cacheHit: true},
		{name: "404", status: http.StatusNotFound, wantStage: "HTTP status 404"},
		{name: "503", status: http.StatusServiceUnavailable, wantStage: "HTTP status 503"},
		{
			name:         "credential-bearing transport error",
			transportErr: errors.New("https://user:password@example.com/spec?sig=secret#fragment"),
			wantStage:    "HTTP request failed",
		},
		{name: "truncated response", status: http.StatusOK, readErr: true, wantStage: "response read failed"},
		{name: "unwritable cache", status: http.StatusOK, writeErr: true, wantStage: "cache write failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			client := newOpenAPIProbeClient(t, &helpersProjectServer{
				project: &azdext.ProjectConfig{Path: root},
			})
			directory := filepath.Join(root, ".azure", "test")
			require.NoError(t, os.MkdirAll(directory, 0o700))
			cachePath := filepath.Join(directory, "openapi-agent-remote.json")
			const spec = `{"openapi":"3.0.0","paths":{}}`
			if test.cacheHit {
				require.NoError(t, os.WriteFile(cachePath, []byte(spec), 0o600))
			}
			if test.writeErr {
				require.NoError(t, os.Mkdir(cachePath, 0o700))
			}
			calls := 0
			headers := http.Header{
				"Authorization": []string{"untrusted"},
				"X-Client-Test": []string{"custom"},
			}
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "session +&/?", request.URL.Query().Get("agent_session_id"))
				assert.Equal(t, "v1", request.URL.Query().Get("api-version"))
				assert.Equal(t, "Bearer managed", request.Header.Get("Authorization"))
				assert.Equal(t, "custom", request.Header.Get("x-client-test"))
				if test.transportErr != nil {
					return nil, test.transportErr
				}
				var reader io.Reader = strings.NewReader(spec)
				if test.readErr {
					reader = brokenInvocationReader{}
				}
				return openAPITestResponse(request, test.status, io.NopCloser(reader)), nil
			})
			path, fresh, err := fetchOpenAPISpec(
				t.Context(), client, transport, "https://example.com", "agent", "remote",
				"managed", "v1", "session +&/?", headers, !test.cacheHit,
			)
			assert.Equal(t, "untrusted", headers.Get("Authorization"))
			if test.wantStage != "" {
				require.ErrorContains(t, err, test.wantStage)
				assert.Empty(t, path)
				assert.False(t, fresh)
				for _, secret := range []string{"password", "sig=", "secret", "fragment", "https://", "user:"} {
					assert.NotContains(t, err.Error(), secret)
				}
				if !test.writeErr {
					_, cacheErr := os.Stat(cachePath)
					assert.ErrorIs(t, cacheErr, os.ErrNotExist)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, cachePath, path)
				assert.Equal(t, !test.cacheHit, fresh)
				data, readErr := os.ReadFile(cachePath)
				require.NoError(t, readErr)
				assert.Equal(t, spec, string(data))
			}
			if test.cacheHit {
				assert.Zero(t, calls)
			} else {
				assert.Equal(t, 1, calls)
			}
		})
	}
}

func TestOpenAPIProbeRedirectPolicy(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, target := range []string{"/redirected", "https://other.example/redirected"} {
			for _, suffix := range []string{"remote", "local"} {
				t.Run(fmt.Sprintf("%d/%s/%s", code, target, suffix), func(t *testing.T) {
					root := t.TempDir()
					client := newOpenAPIProbeClient(t, &helpersProjectServer{
						project: &azdext.ProjectConfig{Path: root},
					})
					directory := filepath.Join(root, ".azure", "test")
					require.NoError(t, os.MkdirAll(directory, 0o700))
					redirectBody := &openAPIResponseBody{Reader: strings.NewReader("redirect")}
					requests := 0
					headers := http.Header{
						"X-Client-Test":      []string{"private-client"},
						"X-Ms-User-Identity": []string{"private-user"},
					}
					sid, token := "private-session", "private-token"
					if suffix == "local" {
						headers, sid, token = nil, "", ""
					}
					transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
						requests++
						if requests == 1 {
							assert.Equal(t, sid, req.URL.Query().Get("agent_session_id"))
							if suffix == "remote" {
								assert.Equal(t, "private-client", req.Header.Get("x-client-test"))
								assert.Equal(t, "private-user", req.Header.Get("x-ms-user-identity"))
								assert.Equal(t, "Bearer "+token, req.Header.Get("Authorization"))
							}
							resp := openAPITestResponse(req, code, redirectBody)
							resp.Header.Set("Location", target)
							return resp, nil
						}
						assert.Equal(t, "local", suffix, "remote probe followed a redirect")
						return openAPITestResponse(req, 200, io.NopCloser(strings.NewReader("{}"))), nil
					})
					path, fresh, err := fetchOpenAPISpec(
						t.Context(), client, transport, "https://example.com", "agent", suffix,
						token, "", sid, headers, true,
					)
					assert.True(t, redirectBody.closed)
					if suffix == "remote" {
						require.ErrorContains(t, err, fmt.Sprintf("HTTP status %d", code))
						assert.Equal(t, 1, requests)
						assert.Empty(t, path)
						assert.False(t, fresh)
						for _, private := range []string{target, sid, token, "private-client", "private-user"} {
							assert.NotContains(t, err.Error(), private)
						}
						_, cacheErr := os.Stat(filepath.Join(directory, "openapi-agent-remote.json"))
						assert.ErrorIs(t, cacheErr, os.ErrNotExist)
					} else {
						require.NoError(t, err)
						assert.Equal(t, 2, requests)
						assert.True(t, fresh)
					}
				})
			}
		}
	}
}

type brokenInvocationReader struct{}

func (brokenInvocationReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

type openAPIProbeParentContext struct {
	context.Context
	configRegistered atomic.Bool
	configCanceled   atomic.Bool
}

func (c *openAPIProbeParentContext) Value(any) any {
	// Use AfterFunc instead of the embedded context's cancellation tree.
	return nil
}

func (c *openAPIProbeParentContext) AfterFunc(f func()) func() bool {
	stop := context.AfterFunc(c.Context, f)
	// The first child owns the configuration timeout.
	if !c.configRegistered.CompareAndSwap(false, true) {
		return stop
	}
	return func() bool {
		c.configCanceled.Store(true)
		return stop()
	}
}

func TestOpenAPIProbeCancellationAndBudgets(t *testing.T) {
	require.Equal(t, 10*time.Second, openAPIProbeTimeout)
	for _, stage := range []string{
		"already canceled", "configuration", "HTTP", "configuration deadline", "HTTP deadline", "separate budgets",
	} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".azure", "test"), 0o700))
			ctx, cancel := context.WithCancel(t.Context())
			if strings.Contains(stage, "deadline") {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			}
			defer cancel()
			parent := &openAPIProbeParentContext{Context: ctx}
			var configCalled atomic.Bool
			project := &openAPIProjectServer{
				helpersProjectServer: helpersProjectServer{project: &azdext.ProjectConfig{Path: root}},
				onGet: func(rpcCtx context.Context) error {
					configCalled.Store(true)
					deadline, ok := rpcCtx.Deadline()
					assert.True(t, ok)
					assert.LessOrEqual(t, time.Until(deadline), openAPIProbeTimeout)
					if strings.HasPrefix(stage, "configuration") {
						if stage == "configuration" {
							cancel()
						}
						<-rpcCtx.Done()
						return rpcCtx.Err()
					}
					return nil
				},
			}
			client := newOpenAPIProbeClient(t, project)
			httpCalled := false
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				httpCalled = true
				deadline, ok := request.Context().Deadline()
				assert.True(t, ok)
				if stage == "separate budgets" || stage == "HTTP" {
					// Releasing the config budget must not cancel HTTP.
					assert.True(t, parent.configRegistered.Load())
					assert.True(t, parent.configCanceled.Load())
				}
				assert.LessOrEqual(t, time.Until(deadline), openAPIProbeTimeout)
				require.NoError(t, request.Context().Err())
				if strings.HasPrefix(stage, "HTTP") {
					if stage == "HTTP" {
						cancel()
					}
					<-request.Context().Done()
					return nil, request.Context().Err()
				}
				return openAPITestResponse(request, http.StatusOK, io.NopCloser(strings.NewReader("{}"))), nil
			})
			if stage == "already canceled" {
				cancel()
			}
			_, _, err := fetchOpenAPISpec(
				parent, client, transport, "https://example.com", "agent", "remote", "", "", "sid", nil, false,
			)
			if stage == "separate budgets" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			assert.Equal(t, stage != "already canceled", configCalled.Load())
			assert.Equal(t, strings.HasPrefix(stage, "HTTP") || stage == "separate budgets", httpCalled)
		})
	}
}

func TestOpenAPIProbeMissingSessionSkipsConfiguration(t *testing.T) {
	project := &helpersProjectServer{err: errors.New("configuration must not be accessed")}
	client := newOpenAPIProbeClient(t, project)
	path, fresh, err := fetchOpenAPISpec(t.Context(), client, nil, "", "agent", "remote", "", "", "", nil, false)
	require.NoError(t, err)
	assert.Empty(t, path)
	assert.False(t, fresh)
}

func TestInvocationsRemoteProbeResponseModes(t *testing.T) {
	previousInterval := defaultLROPollInterval
	defaultLROPollInterval = time.Millisecond
	t.Cleanup(func() { defaultLROPollInterval = previousInterval })
	for _, test := range []struct {
		name               string
		status             int
		body               string
		invocationIDHeader bool
		sse                bool
		readErr            bool
		closeErr           error
		cancel             bool
		probeCancel        bool
		probeTransportErr  bool
		metadata           int
		wantError          string
		wantProbe          bool
		lroFailure         bool
	}{
		{name: "sync", status: 200, body: `{"result":"ok"}`, wantProbe: true},
		{name: "sync read error", status: 200, readErr: true, wantError: "unexpected EOF"},
		{
			name: "sync cleanup failure", status: 200, body: `{"result":"ok"}`,
			closeErr: errors.New("response cleanup failed"), wantProbe: true,
		},
		{name: "agent error", status: 200, body: `{"error":{"message":"bad"}}`, wantError: "agent error"},
		{name: "SSE", status: 200, sse: true, body: "data: first\n\ndata: [DONE]\n\n", wantProbe: true},
		{
			name: "SSE agent error", status: 200, sse: true,
			body: "data: {\"error\":{\"message\":\"bad\"}}\n\n", wantError: "agent error",
		},
		{name: "SSE read error", status: 200, sse: true, readErr: true, wantError: "reading"},
		{name: "LRO", status: 202, body: `{"invocation_id":"inv-1","status":"running"}`, wantProbe: true},
		{
			name: "LRO header", status: 202, body: `{"status":"running"}`,
			invocationIDHeader: true, wantProbe: true,
		},
		{
			name: "LRO cleanup failure", status: 202, body: `{"invocation_id":"inv-1","status":"running"}`,
			closeErr: errors.New("response cleanup failed"), wantProbe: true,
		},
		{
			name: "LRO failure", status: 202, body: `{"invocation_id":"inv-1","status":"running"}`,
			lroFailure: true, wantError: "invocation failed",
		},
		{
			name: "LRO header failure", status: 202, body: `{"status":"running"}`,
			invocationIDHeader: true, lroFailure: true, wantError: "invocation failed",
		},
		{
			name: "missing invocation ID", status: 202, body: `{"status":"running"}`,
			wantError: "no invocation ID",
		},
		{name: "unreadable 202", status: 202, readErr: true, wantError: "read accepted"},
		{name: "canceled successful body", status: 200, body: `{"result":"ok"}`, cancel: true},
		{name: "optional metadata failure", status: 200, body: `{"result":"ok"}`, metadata: 503, wantProbe: true},
		{name: "optional metadata redirect", status: 200, body: `{"result":"ok"}`, metadata: 302, wantProbe: true},
		{
			name: "optional metadata transport failure", status: 200, body: `{"result":"ok"}`,
			probeTransportErr: true, wantProbe: true,
		},
		{name: "optional metadata cancellation", status: 200, body: `{"result":"ok"}`, probeCancel: true, wantProbe: true},
		{name: "HTTP failure", status: 503, body: `{}`, wantError: "503"},
	} {
		for _, format := range []string{"default", outputRaw} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.MkdirAll(filepath.Join(root, ".azure", "test"), 0o700))
				client := newOpenAPIProbeClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{Path: root}})
				observer, err := azdext.NewAzdClient()
				require.NoError(t, err)
				defer observer.Close()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var reader io.Reader = strings.NewReader(test.body)
				if test.readErr {
					reader = brokenInvocationReader{}
				}
				postBody := &openAPIResponseBody{Reader: reader, closeErr: test.closeErr, onRead: func() {
					sid, getErr := getAgentSpecificContextValue(t.Context(), observer, "sessions", "agent-key")
					require.NoError(t, getErr)
					assert.Equal(t, "assigned-session", sid)
					if test.cancel {
						cancel()
					}
				}}
				var paths []string
				polls := 0
				terminalRead := false
				transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
					paths = append(paths, request.URL.Path)
					switch {
					case request.Method == http.MethodPost:
						response := openAPITestResponse(request, test.status, postBody)
						response.Header.Set("x-agent-session-id", "assigned-session")
						if test.invocationIDHeader {
							response.Header.Set("x-agent-invocation-id", "inv-1")
						}
						if test.sse {
							response.Header.Set("Content-Type", "text/event-stream")
						}
						return response, nil
					case strings.HasSuffix(request.URL.Path, "/docs/openapi.json"):
						assert.True(t, postBody.closed)
						assert.Equal(t, 1, postBody.closeCalls)
						if test.status == http.StatusAccepted {
							assert.Equal(t, 2, polls)
							assert.True(t, terminalRead)
						}
						assert.Equal(t, "assigned-session", request.URL.Query().Get("agent_session_id"))
						assert.Equal(t, "user-1", request.Header.Get("x-ms-user-identity"))
						assert.Equal(t, "custom", request.Header.Get("x-client-test"))
						assert.Empty(t, request.Header.Get("Content-Type"))
						assert.Empty(t, request.Header.Get("Accept-Encoding"))
						assert.Empty(t, request.Header.Get("x-agent-foundry-call-id"))
						if test.probeCancel {
							cancel()
							<-request.Context().Done()
							return nil, request.Context().Err()
						}
						if test.probeTransportErr {
							return nil, errors.New("metadata transport unavailable")
						}
						status := test.metadata
						if status == 0 {
							status = http.StatusNotFound
						}
						response := openAPITestResponse(request, status, io.NopCloser(strings.NewReader("metadata-only")))
						response.Header.Set("x-agent-session-id", "unrelated-session")
						if status == http.StatusFound {
							response.Header.Set("Location", "/redirected")
						}
						return response, nil
					default:
						assert.True(t, strings.HasSuffix(request.URL.Path, "/invocations/inv-1"))
						polls++
						if polls == 1 {
							return openAPITestResponse(
								request, 200, io.NopCloser(strings.NewReader(`{"status":"running"}`)),
							), nil
						}
						body := `{"status":"completed","result":"terminal"}`
						if test.lroFailure {
							body = `{"status":"failed"}`
						}
						terminalBody := &openAPIResponseBody{
							Reader: strings.NewReader(body),
							onRead: func() { terminalRead = true },
						}
						return openAPITestResponse(request, 200, terminalBody), nil
					}
				})
				action := &InvokeAction{
					flags: &invokeFlags{
						message: "{}", newSession: true, outputFmt: format,
						userIdentityFlags: userIdentityFlags{userIdentity: "user-1"},
					},
					clientHeaders:        http.Header{"X-Client-Test": []string{"custom"}},
					credential:           responseTestCredential{},
					invocationsTransport: transport,
					resolvedRemoteContext: &remoteContext{
						projectEndpoint: "https://example.com", name: "agent", apiVersion: "v1",
						agentKey: "agent-key", azdClient: client,
					},
				}
				stdout, err := captureStdout(t, func() error { return action.invocationsRemote(ctx) })
				wantError := test.wantError
				wantProbe := test.wantProbe
				if format == outputRaw && strings.Contains(test.name, "agent error") {
					wantError, wantProbe = "", true
				}
				if format == outputRaw && test.name == "SSE read error" {
					wantError = "raw response body"
				}
				if wantError != "" {
					require.ErrorContains(t, err, wantError)
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, 1, postBody.closeCalls)
				probes := 0
				for _, path := range paths {
					if strings.HasSuffix(path, "/docs/openapi.json") {
						probes++
					}
				}
				wantProbes := 0
				if wantProbe {
					wantProbes = 1
				}
				assert.Equal(t, wantProbes, probes)
				assert.NotContains(t, stdout, "metadata-only")
				assert.NotContains(t, stdout, "unrelated-session")
				sid, getErr := getAgentSpecificContextValue(t.Context(), observer, "sessions", "agent-key")
				require.NoError(t, getErr)
				assert.Equal(t, "assigned-session", sid)
			})
		}
	}
}

func TestInvocationsLocalProbeCompatibility(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".azure", "test"), 0o700))
	_ = newOpenAPIProbeClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{Path: root}})
	var methods []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		if request.Method == http.MethodGet {
			assert.Equal(t, "/invocations/docs/openapi.json", request.URL.Path)
			assert.Empty(t, request.URL.RawQuery)
			assert.Empty(t, request.Header)
			return openAPITestResponse(request, 200, io.NopCloser(strings.NewReader(`{"openapi":"3.0.0"}`))), nil
		}
		assert.Equal(t, "local-session", request.URL.Query().Get("agent_session_id"))
		assert.Equal(t, "local-user", request.Header.Get("x-agent-user-id"))
		assert.Equal(t, "call", request.Header.Get("x-agent-foundry-call-id"))
		assert.Equal(t, "custom", request.Header.Get("x-client-test"))
		return openAPITestResponse(request, 200, io.NopCloser(strings.NewReader(`{"result":"ok"}`))), nil
	})
	for range 2 {
		action := &InvokeAction{
			flags: &invokeFlags{
				name: "agent", message: "{}", session: "local-session", callID: "call",
				userIdentityFlags: userIdentityFlags{userIdentity: "local-user"},
			},
			clientHeaders:        http.Header{"X-Client-Test": []string{"custom"}},
			invocationsTransport: transport,
		}
		stdout, err := captureStdout(t, func() error { return action.invocationsLocal(t.Context()) })
		require.NoError(t, err)
		assert.Contains(t, stdout, "OpenAPI spec saved to")
	}
	assert.Equal(t, []string{"GET", "POST", "GET", "POST"}, methods)
	_, err := os.Stat(filepath.Join(root, ".azure", "test", "openapi-agent-local.json"))
	require.NoError(t, err)
}

func TestInvocationsRemoteVersionProbeReusesSelectedSession(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".azure", "test"), 0o700))
	client := newOpenAPIProbeClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{Path: root}})
	previous := createInvokeVersionSession
	t.Cleanup(func() { createInvokeVersionSession = previous })
	creates := 0
	createInvokeVersionSession = func(
		context.Context, string, string, string, string,
	) (*agent_api.AgentSessionResource, error) {
		creates++
		return &agent_api.AgentSessionResource{AgentSessionID: "version-session"}, nil
	}
	var methods []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		assert.Equal(t, "version-session", request.URL.Query().Get("agent_session_id"))
		return openAPITestResponse(request, 200, io.NopCloser(strings.NewReader("{}"))), nil
	})
	for range 2 {
		action := &InvokeAction{
			flags: &invokeFlags{message: "{}", outputFmt: outputRaw}, credential: responseTestCredential{},
			invocationsTransport: transport,
			resolvedRemoteContext: &remoteContext{
				projectEndpoint: "https://example.com", name: "agent", version: "3", apiVersion: "v1",
				agentKey: "version-key", azdClient: client,
			},
		}
		_, err := captureStdout(t, func() error { return action.invocationsRemote(t.Context()) })
		require.NoError(t, err)
		client, err = azdext.NewAzdClient()
		require.NoError(t, err)
		defer client.Close()
	}
	assert.Equal(t, 1, creates)
	assert.Equal(t, []string{"POST", "GET", "POST"}, methods)
}
