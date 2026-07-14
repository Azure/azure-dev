// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
)

type invokeUserConfigServer struct {
	azdext.UnimplementedUserConfigServiceServer
	mu     sync.Mutex
	values map[string][]byte
}

func newInvokeUserConfigServer() *invokeUserConfigServer {
	return &invokeUserConfigServer{values: make(map[string][]byte)}
}

func (s *invokeUserConfigServer) Get(
	_ context.Context,
	req *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	value, found := s.values[req.Path]
	return &azdext.GetUserConfigResponse{Value: value, Found: found}, nil
}

func (s *invokeUserConfigServer) Set(
	_ context.Context,
	req *azdext.SetUserConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.values[req.Path] = req.Value
	return &azdext.EmptyResponse{}, nil
}

func (s *invokeUserConfigServer) setJSON(t *testing.T, path string, value any) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal user config value: %v", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[path] = data
}

func newInvokeTestAzdClient(t *testing.T, userConfigServer azdext.UserConfigServiceServer) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterUserConfigServiceServer(grpcServer, userConfigServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	azdClient, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	if err != nil {
		t.Fatalf("NewAzdClient: %v", err)
	}

	t.Cleanup(func() {
		azdClient.Close()
	})

	return azdClient
}

func TestReadSSEStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{
			name: "text deltas followed by completed",
			input: "event: response.output_text.delta\n" +
				`data: {"delta":"Hello "}` + "\n\n" +
				"event: response.output_text.delta\n" +
				`data: {"delta":"world!"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"response":{"status":"completed","output":[]}}` + "\n\n",
			wantErr: false,
		},
		{
			name: "completed with no deltas and output_text in response",
			input: "event: response.completed\n" +
				`data: {"response":{"status":"completed","output":[{"content":[{"type":"output_text","text":"Hi there"}]}]}}` + "\n\n",
			wantErr: false,
		},
		{
			name: "failed status in completed event",
			input: "event: response.completed\n" +
				`data: {"response":{"status":"failed","error":{"code":"runtime_error","message":"agent crashed"}}}` + "\n\n",
			wantErr: true,
			errMsg:  "agent failed (runtime_error): agent crashed",
		},
		{
			name: "failed status without error details",
			input: "event: response.completed\n" +
				`data: {"response":{"status":"failed"}}` + "\n\n",
			wantErr: true,
			errMsg:  "agent returned failed status",
		},
		{
			name: "error event with structured error",
			input: "event: error\n" +
				`data: {"code":"rate_limit","message":"too many requests"}` + "\n\n",
			wantErr: true,
			errMsg:  "agent error (rate_limit): too many requests",
		},
		{
			name: "error event with unstructured data",
			input: "event: error\n" +
				"data: something went wrong\n\n",
			wantErr: true,
			errMsg:  "agent stream error: something went wrong",
		},
		{
			name:    "empty stream",
			input:   "",
			wantErr: false,
		},
		{
			name:    "blank lines only",
			input:   "\n\n\n",
			wantErr: false,
		},
		{
			name: "unknown event types are ignored",
			input: "event: response.created\n" +
				`data: {"id":"resp_123"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"response":{"status":"completed","output":[]}}` + "\n\n",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := strings.NewReader(tt.input)
			err := readSSEStream(reader, "test-agent")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestPrintAgentResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		result  map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name: "successful output_text",
			result: map[string]any{
				"status": "completed",
				"output": []any{
					map[string]any{
						"content": []any{
							map[string]any{
								"type": "output_text",
								"text": "Hello from agent",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed status with error details",
			result: map[string]any{
				"status": "failed",
				"error": map[string]any{
					"code":    "timeout",
					"message": "agent timed out",
				},
			},
			wantErr: true,
			errMsg:  "agent failed (timeout): agent timed out",
		},
		{
			name: "failed status without error details",
			result: map[string]any{
				"status": "failed",
			},
			wantErr: true,
			errMsg:  "agent returned failed status",
		},
		{
			name: "server error code",
			result: map[string]any{
				"code":    "server_error",
				"message": "internal error",
			},
			wantErr: true,
			errMsg:  "agent error (server_error): internal error",
		},
		{
			name: "no output key prints JSON",
			result: map[string]any{
				"status": "completed",
				"id":     "resp_123",
			},
			wantErr: false,
		},
		{
			name: "empty output array prints JSON",
			result: map[string]any{
				"output": []any{},
			},
			wantErr: false,
		},
		{
			name: "content with non-output_text type is skipped",
			result: map[string]any{
				"output": []any{
					map[string]any{
						"content": []any{
							map[string]any{
								"type": "image",
								"url":  "https://example.com/img.png",
							},
						},
					},
				},
			},
			wantErr: false, // Falls through to JSON print
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := printAgentResponse(tt.result, "test")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestHttpTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		timeout int
		want    time.Duration
	}{
		{name: "default value", timeout: defaultInvokeTimeoutSeconds, want: 30 * time.Minute},
		{name: "positive value", timeout: 120, want: 120 * time.Second},
		{name: "zero means no timeout", timeout: 0, want: 0},
		{name: "negative means no timeout", timeout: -1, want: 0},
		{name: "custom value", timeout: 300, want: 300 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			action := &InvokeAction{
				flags: &invokeFlags{timeout: tt.timeout},
			}
			got := action.httpTimeout()
			if got != tt.want {
				t.Errorf("httpTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInvokeCommandTimeoutDefault(t *testing.T) {
	t.Parallel()

	cmd := newInvokeCommand(nil)
	timeoutFlag := cmd.Flags().Lookup("timeout")
	if timeoutFlag == nil {
		t.Fatal("timeout flag not registered")
	}

	const want = "1800"
	if timeoutFlag.DefValue != want {
		t.Errorf("timeout default = %q, want %q", timeoutFlag.DefValue, want)
	}
	if timeoutFlag.Value.String() != want {
		t.Errorf("timeout value = %q, want %q", timeoutFlag.Value.String(), want)
	}
}

func TestInvokeCommandVersionFlagRegistered(t *testing.T) {
	t.Parallel()

	cmd := newInvokeCommand(nil)
	versionFlag := cmd.Flags().Lookup("version")
	if versionFlag == nil {
		t.Fatal("version flag not registered")
	}
	if versionFlag.DefValue != "" {
		t.Errorf("version default = %q, want empty", versionFlag.DefValue)
	}
}

func TestInvokeVersionFlagValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		args   []string
		errSub string
	}{
		{
			name:   "rejects empty version",
			args:   []string{"--version", "   ", "hi"},
			errSub: "requires a non-empty agent version",
		},
		{
			name:   "rejects local",
			args:   []string{"--version", "3", "--local", "hi"},
			errSub: "cannot use --version with --local",
		},
		{
			name:   "rejects explicit session id",
			args:   []string{"--version", "3", "--session-id", "existing-session", "hi"},
			errSub: "cannot use --version with --session-id",
		},
		{
			name:   "rejects unsupported version characters",
			args:   []string{"--version", "3?api-version=evil", "hi"},
			errSub: "unsupported characters",
		},
		{
			name:   "rejects oversized version",
			args:   []string{"--version", strings.Repeat("a", maxInvokeVersionLength+1), "hi"},
			errSub: "at most 128 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newInvokeCommand(nil)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errSub)
			}
		})
	}
}

func TestInvokeCommand_HasUserIdentityFlag(t *testing.T) {
	t.Parallel()

	cmd := newInvokeCommand(nil)
	flag := cmd.Flags().Lookup("user-identity")
	if flag == nil {
		t.Fatal("user-identity flag not registered")
	}
	if flag.DefValue != "" {
		t.Errorf("user-identity default = %q, want empty", flag.DefValue)
	}
}

// TestInvokeLocalWithNamedAgent verifies that --local combined with a
// positional agent name passes validation (no "cannot use --local with a
// named agent" error). The command will still fail at Run time (no azd
// project), but the validation stage must not reject it.
func TestInvokeLocalWithNamedAgent(t *testing.T) {
	t.Parallel()

	cmd := newInvokeCommand(nil)
	cmd.SetArgs([]string{"my-agent", "--local", "Hello!"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()

	if err == nil {
		t.Fatal("expected an error (no azd project), got nil")
	}
	if strings.Contains(err.Error(), "cannot use --local with a named agent") {
		t.Fatalf("unexpected validation rejection: %v", err)
	}
}

func TestUnresolvedRemoteAgentNameError(t *testing.T) {
	t.Parallel()

	t.Run("resolved service has not been deployed", func(t *testing.T) {
		t.Parallel()

		err := unresolvedRemoteAgentNameError("my-agent-service")
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		if !ok {
			t.Fatalf("error type = %T, want *azdext.LocalError", err)
		}
		if !strings.Contains(localErr.Message, "does not appear to have been deployed") {
			t.Errorf("message = %q, want deploy-state guidance", localErr.Message)
		}
		if !strings.Contains(localErr.Suggestion, "azd deploy") {
			t.Errorf("suggestion = %q, want azd deploy guidance", localErr.Suggestion)
		}
		if localErr.Code != exterrors.CodeMissingAgentEnvVars {
			t.Errorf("code = %q, want %q", localErr.Code, exterrors.CodeMissingAgentEnvVars)
		}
		if localErr.Category != azdext.LocalErrorCategoryDependency {
			t.Errorf("category = %q, want %q", localErr.Category, azdext.LocalErrorCategoryDependency)
		}
	})

	t.Run("no service resolved", func(t *testing.T) {
		t.Parallel()

		err := unresolvedRemoteAgentNameError("")
		if !strings.Contains(err.Error(), "agent name is required") {
			t.Errorf("error = %q, want missing-name guidance", err)
		}
	})
}

func TestUserIdentityFlags_SessionRequestOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flags    *userIdentityFlags
		wantNil  bool
		wantUser string
	}{
		{
			name:     "user identity set",
			flags:    &userIdentityFlags{userIdentity: "u"},
			wantUser: "u",
		},
		{
			name:    "empty user identity returns nil",
			flags:   &userIdentityFlags{},
			wantNil: true,
		},
		{
			name:    "nil flags returns nil",
			flags:   nil,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := tt.flags.sessionRequestOptions()
			if tt.wantNil {
				if opts != nil {
					t.Errorf("sessionRequestOptions() = %+v, want nil", opts)
				}
				return
			}
			if opts == nil {
				t.Fatal("sessionRequestOptions() returned nil, want non-nil")
			}
			if opts.UserIdentity != tt.wantUser {
				t.Errorf("UserIdentity = %q, want %q", opts.UserIdentity, tt.wantUser)
			}
		})
	}
}

func TestValidateInvokeVersionFlagsAllowsConversationFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags *invokeFlags
	}{
		{
			name: "explicit conversation id",
			flags: &invokeFlags{
				version:      "3",
				conversation: "existing-conversation",
			},
		},
		{
			name: "new conversation",
			flags: &invokeFlags{
				version:         "3",
				newConversation: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newInvokeCommand(nil)
			if err := validateInvokeVersionFlags(cmd, tt.flags); err != nil {
				t.Fatalf("validateInvokeVersionFlags rejected %s with --version: %v", tt.name, err)
			}
		})
	}
}

func TestAgentEndpointAllowsVersionFlag(t *testing.T) {
	t.Parallel()

	cmd := newInvokeCommand(nil)
	err := validateAgentEndpointFlags(cmd, &invokeFlags{
		agentEndpoint: "https://acct.services.ai.azure.com/api/projects/proj/agents/" +
			"hello/endpoint/protocols/invocations",
		version: "3",
	})
	if err != nil {
		t.Fatalf("validateAgentEndpointFlags rejected --version: %v", err)
	}
}

func TestValidateInvokeVersionValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{name: "integer", version: "3"},
		{name: "semver", version: "1.2.3-beta_1"},
		{name: "question mark", version: "3?api-version=evil", wantErr: true},
		{name: "slash", version: "path/to/version", wantErr: true},
		{name: "unicode", version: "v三", wantErr: true},
		{name: "too long", version: strings.Repeat("a", maxInvokeVersionLength+1), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateInvokeVersionValue(tt.version)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResolveProtocol_ExplicitFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		protocol string
		want     agent_api.AgentProtocol
	}{
		{
			name:     "explicit invocations",
			protocol: "invocations",
			want:     agent_api.AgentProtocolInvocations,
		},
		{
			name:     "explicit responses",
			protocol: "responses",
			want:     agent_api.AgentProtocolResponses,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			action := &InvokeAction{
				flags: &invokeFlags{protocol: tt.protocol},
			}
			// resolveProtocol with an explicit flag should return it directly
			// without trying to read agent.yaml (which would fail in tests).
			got, err := action.resolveProtocol(t.Context())
			if err != nil {
				t.Fatalf("resolveProtocol() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveProtocol() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProtocolFlagValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errSub  string
	}{
		{
			name:    "valid responses",
			args:    []string{"--protocol", "responses", "hello"},
			wantErr: false,
		},
		{
			name:    "valid invocations",
			args:    []string{"--protocol", "invocations", "hello"},
			wantErr: false,
		},
		{
			name:    "invalid protocol",
			args:    []string{"--protocol", "bogus", "hello"},
			wantErr: true,
			errSub:  "unsupported protocol",
		},
		{
			name:    "rejects --client-header with a2a",
			args:    []string{"--protocol", "a2a", "--client-header", "x-client-request-id: abc", "hello"},
			wantErr: true,
			errSub:  "--client-header is not supported with the a2a protocol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newInvokeCommand(nil)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errSub) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errSub)
				}
			}
			// For valid protocols the command will still fail (no azd host),
			// but the error should NOT be about an invalid protocol.
			if !tt.wantErr && err != nil && strings.Contains(err.Error(), "unsupported protocol") {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

// TestAgentEndpointFlagValidation covers the up-front validation rules for --agent-endpoint.
// These run before any network call, so they exercise the cobra RunE error path directly.
func TestAgentEndpointFlagValidation(t *testing.T) {
	t.Parallel()

	const validURL = "https://acct.services.ai.azure.com/api/projects/proj/agents/" +
		"hello/endpoint/protocols/invocations?api-version=v1"

	tests := []struct {
		name    string
		args    []string
		wantErr bool
		errSub  string
	}{
		{
			name:    "rejects --local",
			args:    []string{"--agent-endpoint", validURL, "--local", "hi"},
			wantErr: true,
			errSub:  "cannot be combined with --local",
		},
		{
			name:    "rejects positional name",
			args:    []string{"--agent-endpoint", validURL, "myagent", "hi"},
			wantErr: true,
			errSub:  "positional agent name",
		},
		{
			name:    "rejects --port",
			args:    []string{"--agent-endpoint", validURL, "--port", "9999", "hi"},
			wantErr: true,
			errSub:  "cannot be combined with --port",
		},
		{
			name:    "rejects explicit --port at default value",
			args:    []string{"--agent-endpoint", validURL, "--port", "8088", "hi"},
			wantErr: true,
			errSub:  "cannot be combined with --port",
		},
		{
			name:    "rejects --protocol",
			args:    []string{"--agent-endpoint", validURL, "--protocol", "responses", "hi"},
			wantErr: true,
			errSub:  "cannot be combined with --protocol",
		},
		{
			name:    "rejects --protocol even when matching",
			args:    []string{"--agent-endpoint", validURL, "--protocol", "invocations", "hi"},
			wantErr: true,
			errSub:  "cannot be combined with --protocol",
		},
		{
			name:    "rejects malformed url",
			args:    []string{"--agent-endpoint", "https://evil.com/foo", "hi"},
			wantErr: true,
			errSub:  "Foundry host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newInvokeCommand(nil)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errSub)
				}
			}
		})
	}
}

func TestResolveRemoteSessionID_ReusesCachedVersionSession(t *testing.T) {
	orig := createInvokeVersionSession
	t.Cleanup(func() { createInvokeVersionSession = orig })

	createInvokeVersionSession = func(
		context.Context,
		string,
		string,
		string,
		string,
	) (*agent_api.AgentSessionResource, error) {
		t.Fatal("createInvokeVersionSession should not be called when a cached session exists")
		return nil, nil
	}

	userConfig := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, userConfig)
	projectEndpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	agentKey := buildAgentKey(projectEndpoint, "hello", "3", false)
	userConfig.setJSON(t, configPath("sessions"), map[string]string{agentKey: "cached-session"})

	action := &InvokeAction{flags: &invokeFlags{version: "3"}}
	rc := &remoteContext{
		name:            "hello",
		projectEndpoint: projectEndpoint,
		version:         "3",
		agentKey:        agentKey,
		azdClient:       azdClient,
	}

	sid, err := action.resolveRemoteSessionID(t.Context(), rc)
	if err != nil {
		t.Fatalf("resolveRemoteSessionID: %v", err)
	}
	if sid != "cached-session" {
		t.Errorf("session id = %q, want cached-session", sid)
	}
}

func TestResolveRemoteSessionID_NewSessionSkipsCachedVersionSession(t *testing.T) {
	orig := createInvokeVersionSession
	t.Cleanup(func() { createInvokeVersionSession = orig })

	var calls int
	createInvokeVersionSession = func(
		context.Context,
		string,
		string,
		string,
		string,
	) (*agent_api.AgentSessionResource, error) {
		calls++
		return &agent_api.AgentSessionResource{AgentSessionID: "fresh-session"}, nil
	}

	userConfig := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, userConfig)
	projectEndpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	agentKey := buildAgentKey(projectEndpoint, "hello", "3", false)
	userConfig.setJSON(t, configPath("sessions"), map[string]string{agentKey: "cached-session"})

	action := &InvokeAction{flags: &invokeFlags{version: "3", newSession: true}}
	rc := &remoteContext{
		name:            "hello",
		projectEndpoint: projectEndpoint,
		version:         "3",
		agentKey:        agentKey,
		azdClient:       azdClient,
	}

	sid, err := action.resolveRemoteSessionID(t.Context(), rc)
	if err != nil {
		t.Fatalf("resolveRemoteSessionID: %v", err)
	}
	if sid != "fresh-session" {
		t.Errorf("session id = %q, want fresh-session", sid)
	}
	if calls != 1 {
		t.Errorf("createInvokeVersionSession calls = %d, want 1", calls)
	}
}

func TestResolveRemoteSessionID_CreatesSessionForExplicitVersion(t *testing.T) {
	orig := createInvokeVersionSession
	t.Cleanup(func() { createInvokeVersionSession = orig })

	var calls int
	createInvokeVersionSession = func(
		ctx context.Context,
		projectEndpoint string,
		agentName string,
		agentVersion string,
		apiVersion string,
	) (*agent_api.AgentSessionResource, error) {
		calls++
		if projectEndpoint != "https://acct.services.ai.azure.com/api/projects/proj" {
			t.Errorf("projectEndpoint = %q", projectEndpoint)
		}
		if agentName != "hello" {
			t.Errorf("agentName = %q", agentName)
		}
		if agentVersion != "3" {
			t.Errorf("agentVersion = %q", agentVersion)
		}
		if apiVersion != "custom-version" {
			t.Errorf("apiVersion = %q", apiVersion)
		}
		return &agent_api.AgentSessionResource{AgentSessionID: "session-v3"}, nil
	}

	action := &InvokeAction{flags: &invokeFlags{version: "3"}}
	rc := &remoteContext{
		name:            "hello",
		projectEndpoint: "https://acct.services.ai.azure.com/api/projects/proj",
		version:         "3",
		apiVersion:      "custom-version",
		agentKey:        buildAgentKey("https://acct.services.ai.azure.com/api/projects/proj", "hello", "3", false),
	}

	sid, err := action.resolveRemoteSessionID(t.Context(), rc)
	if err != nil {
		t.Fatalf("resolveRemoteSessionID: %v", err)
	}
	if sid != "session-v3" {
		t.Errorf("session id = %q, want session-v3", sid)
	}
	if calls != 1 {
		t.Errorf("createInvokeVersionSession calls = %d, want 1", calls)
	}
}

func TestResolveRemoteSessionID_ExplicitSessionPreservedWithoutVersion(t *testing.T) {
	t.Parallel()

	action := &InvokeAction{flags: &invokeFlags{session: "existing-session"}}
	rc := &remoteContext{
		name:            "hello",
		projectEndpoint: "https://acct.services.ai.azure.com/api/projects/proj",
	}

	sid, err := action.resolveRemoteSessionID(t.Context(), rc)
	if err != nil {
		t.Fatalf("resolveRemoteSessionID: %v", err)
	}
	if sid != "existing-session" {
		t.Errorf("session id = %q, want existing-session", sid)
	}
}

func TestVersionedConversationLookupDoesNotUseLegacyAgentName(t *testing.T) {
	t.Parallel()

	userConfig := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, userConfig)
	projectEndpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	versionedKey := buildAgentKey(projectEndpoint, "hello", "3", false)
	userConfig.setJSON(t, configPath("conversations"), map[string]string{"hello": "legacy-conversation"})

	rc := &remoteContext{name: "hello", version: "3"}
	got, err := getContextValueWithFallback(
		t.Context(),
		azdClient,
		"conversations",
		versionedKey,
		rc.legacyKeys(),
	)
	if err != nil {
		t.Fatalf("getContextValueWithFallback: %v", err)
	}
	if got != "" {
		t.Errorf("conversation = %q, want empty because versioned lookup must not use legacy fallback", got)
	}
}

func TestVersionedExplicitConversationPersistsUnderVersionKey(t *testing.T) {
	t.Parallel()

	userConfig := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, userConfig)
	projectEndpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	versionedKey := buildAgentKey(projectEndpoint, "hello", "3", false)

	got, err := resolveConversationID(
		t.Context(),
		azdClient,
		versionedKey,
		"existing-conversation",
		false,
		projectEndpoint,
		"token",
		"hello",
		"v1",
		nil,
	)
	if err != nil {
		t.Fatalf("resolveConversationID: %v", err)
	}
	if got != "existing-conversation" {
		t.Fatalf("conversation = %q, want existing-conversation", got)
	}

	userConfig.mu.Lock()
	defer userConfig.mu.Unlock()

	var conversations map[string]string
	if err := json.Unmarshal(userConfig.values[configPath("conversations")], &conversations); err != nil {
		t.Fatalf("unmarshal persisted conversations: %v", err)
	}
	if got := conversations[versionedKey]; got != "existing-conversation" {
		t.Fatalf("persisted conversation = %q, want existing-conversation", got)
	}
}

func TestRemoteContextLegacyKeysSkippedForExplicitVersion(t *testing.T) {
	t.Parallel()

	rc := &remoteContext{name: "hello", version: "3"}
	if got := rc.legacyKeys(); len(got) != 0 {
		t.Errorf("legacyKeys = %v, want none for explicit version", got)
	}

	rc.version = ""
	if got := rc.legacyKeys(); len(got) != 1 || got[0] != "hello" {
		t.Errorf("legacyKeys = %v, want [hello]", got)
	}
}

func TestHandleInvocationSync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "json result",
			body:    `{"result": "hello"}`,
			wantErr: false,
		},
		{
			name:    "plain text result",
			body:    "Hello from agent",
			wantErr: false,
		},
		{
			name:    "error envelope with code",
			body:    `{"error": {"code": "bad_request", "message": "invalid input"}}`,
			wantErr: true,
			errMsg:  "agent error (bad_request): invalid input",
		},
		{
			name:    "error envelope with type",
			body:    `{"error": {"type": "validation_error", "message": "missing field"}}`,
			wantErr: true,
			errMsg:  "agent error (validation_error): missing field",
		},
		{
			name:    "error envelope with no code or type",
			body:    `{"error": {"message": "something went wrong"}}`,
			wantErr: true,
			errMsg:  "agent error: something went wrong",
		},
		{
			name:    "empty body",
			body:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := strings.NewReader(tt.body)
			err := handleInvocationSync(reader, "test-agent")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHandleInvocationSSE(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		agentName  string
		wantErr    bool
		errMsg     string
		wantOutput string
	}{
		{
			name:       "simple data lines produce separate output lines with prefix",
			input:      "data: Hello \ndata: world!\n\n",
			agentName:  "test-agent",
			wantOutput: "[test-agent] Hello \nworld!\n",
		},
		{
			name:       "single data line gets prefix and newline",
			input:      "data: only-one\n\n",
			agentName:  "my-bot",
			wantOutput: "[my-bot] only-one\n",
		},
		{
			name:       "DONE signal ends stream, only preceding data printed",
			input:      "data: Hello\ndata: [DONE]\ndata: ignored\n\n",
			agentName:  "test-agent",
			wantOutput: "[test-agent] Hello\n",
		},
		{
			name:      "error envelope in data",
			input:     `data: {"error": {"code": "rate_limit", "message": "too many requests"}}` + "\n\n",
			agentName: "test-agent",
			wantErr:   true,
			errMsg:    "agent error (rate_limit): too many requests",
		},
		{
			name:      "error envelope with type only",
			input:     `data: {"error": {"type": "server_error", "message": "crash"}}` + "\n\n",
			agentName: "test-agent",
			wantErr:   true,
			errMsg:    "agent error (server_error): crash",
		},
		{
			name:       "empty stream produces no output",
			input:      "",
			agentName:  "test-agent",
			wantOutput: "",
		},
		{
			name:       "non-data lines ignored",
			input:      "event: custom\nid: 123\ndata: content\n\n",
			agentName:  "test-agent",
			wantOutput: "[test-agent] content\n",
		},
		{
			name:       "three data lines produce three output lines",
			input:      "data: line1\ndata: line2\ndata: line3\n\n",
			agentName:  "agent",
			wantOutput: "[agent] line1\nline2\nline3\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			reader := strings.NewReader(tt.input)
			err := handleInvocationSSE(&buf, reader, tt.agentName)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got := buf.String(); got != tt.wantOutput {
					t.Errorf("output mismatch\ngot:  %q\nwant: %q", got, tt.wantOutput)
				}
			}
		})
	}
}

func TestHandleInvocationResponse_Routing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statusCode  int
		contentType string
		body        string
		headers     map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name:        "200 with JSON routes to sync",
			statusCode:  200,
			contentType: "application/json",
			body:        `{"result": "ok"}`,
			wantErr:     false,
		},
		{
			name:        "200 with SSE routes to streaming",
			statusCode:  200,
			contentType: "text/event-stream",
			body:        "data: hello\n\n",
			wantErr:     false,
		},
		{
			name:        "202 without invocation ID returns error",
			statusCode:  202,
			contentType: "application/json",
			body:        `{"status": "accepted"}`,
			wantErr:     true,
			errContains: "no invocation ID found",
		},
		{
			name:        "400 returns HTTP error",
			statusCode:  400,
			contentType: "application/json",
			body:        `{"error": "bad request"}`,
			wantErr:     true,
			errContains: "HTTP 400",
		},
		{
			name:        "500 returns HTTP error",
			statusCode:  500,
			contentType: "text/plain",
			body:        "Internal Server Error",
			wantErr:     true,
			errContains: "HTTP 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{
				StatusCode: tt.statusCode,
				Status:     http.StatusText(tt.statusCode),
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(tt.body)),
				Request:    &http.Request{},
			}
			resp.Header.Set("Content-Type", tt.contentType)
			for k, v := range tt.headers {
				resp.Header.Set(k, v)
			}

			err := handleInvocationResponse(t.Context(), resp, "", "", "test-agent", 10*time.Second, "", nil, false)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResolveBody(t *testing.T) {
	t.Parallel()

	t.Run("message string", func(t *testing.T) {
		t.Parallel()

		action := &InvokeAction{
			flags: &invokeFlags{message: "Hello!"},
		}
		body, label, err := action.resolveBody()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(body) != "Hello!" {
			t.Errorf("body = %q, want %q", string(body), "Hello!")
		}
		if !strings.Contains(label, "Hello!") {
			t.Errorf("label = %q, want it to contain the message", label)
		}
	})

	t.Run("input file", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "request.json")
		content := `{"task": "summarize", "text": "long text..."}`
		if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
			t.Fatalf("failed to write temp file: %v", err)
		}

		action := &InvokeAction{
			flags: &invokeFlags{inputFile: filePath},
		}
		body, label, err := action.resolveBody()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(body) != content {
			t.Errorf("body = %q, want %q", string(body), content)
		}
		if !strings.Contains(label, "request.json") {
			t.Errorf("label = %q, want it to contain the filename", label)
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		t.Parallel()

		action := &InvokeAction{
			flags: &invokeFlags{inputFile: "/nonexistent/path/file.json"},
		}
		_, _, err := action.resolveBody()
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
		if !strings.Contains(err.Error(), "failed to read input file") {
			t.Errorf("error = %q, want it to mention failed to read", err.Error())
		}
	})
}

func TestHandleInvocationLRO(t *testing.T) {
	// Override poll interval for fast tests. Not parallel since we modify a package var.
	origInterval := defaultLROPollInterval
	defaultLROPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { defaultLROPollInterval = origInterval })

	tests := []struct {
		name string
		// initial202Header sets the x-agent-invocation-id on the 202 response.
		// If empty, initial202Body should contain invocation_id.
		initial202Header string
		initial202Body   string
		// pollResponses is a sequence of responses the poll server returns.
		// Each entry is a status code + body pair.
		pollResponses []pollStep
		timeout       time.Duration
		wantErr       bool
		errContains   string
	}{
		{
			name:             "happy path — completed on first poll",
			initial202Header: "inv-001",
			initial202Body:   `{"status":"accepted"}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"completed","result":"done"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
		{
			name:             "invocation ID from body when header is missing",
			initial202Header: "",
			initial202Body:   `{"invocation_id":"inv-from-body","status":"accepted"}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"completed","result":"ok"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
		{
			name:             "poll returns running then completed",
			initial202Header: "inv-002",
			initial202Body:   `{"status":"accepted"}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"running"}`},
				{status: 200, body: `{"status":"completed","result":"done"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
		{
			name:             "poll returns 404 then completed",
			initial202Header: "inv-003",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 404, body: "not found"},
				{status: 200, body: `{"status":"completed","result":"ok"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
		{
			name:             "poll returns failed with error details",
			initial202Header: "inv-004",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"failed","error":{"code":"runtime_error","message":"agent crashed"}}`},
			},
			timeout:     10 * time.Second,
			wantErr:     true,
			errContains: "invocation failed (runtime_error): agent crashed",
		},
		{
			name:             "poll returns cancelled",
			initial202Header: "inv-005",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"cancelled"}`},
			},
			timeout:     10 * time.Second,
			wantErr:     true,
			errContains: "invocation was cancelled",
		},
		{
			name:             "poll returns HTTP 500 error",
			initial202Header: "inv-006",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 500, body: "Internal Server Error"},
			},
			timeout:     10 * time.Second,
			wantErr:     true,
			errContains: "HTTP 500",
		},
		{
			name:             "timeout waiting for completion",
			initial202Header: "inv-007",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				// Always returns running — will repeat until timeout
				{status: 200, body: `{"status":"running"}`, repeat: true},
			},
			timeout:     100 * time.Millisecond,
			wantErr:     true,
			errContains: "timed out",
		},
		{
			name:             "no timeout polls until completion",
			initial202Header: "inv-009",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"running"}`},
				{status: 200, body: `{"status":"running"}`},
				{status: 200, body: `{"status":"completed","result":"ok"}`},
			},
			timeout: 0,
			wantErr: false,
		},
		{
			name:             "retry-after header is respected",
			initial202Header: "inv-008",
			initial202Body:   `{}`,
			pollResponses: []pollStep{
				{status: 200, body: `{"status":"running"}`, retryAfter: "1"},
				{status: 200, body: `{"status":"completed","result":"ok"}`},
			},
			timeout: 10 * time.Second,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pollIndex := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if pollIndex >= len(tt.pollResponses) {
					// Repeat last response if marked as repeating
					last := tt.pollResponses[len(tt.pollResponses)-1]
					if last.repeat {
						if last.retryAfter != "" {
							w.Header().Set("Retry-After", last.retryAfter)
						}
						w.WriteHeader(last.status)
						_, _ = w.Write([]byte(last.body))
						return
					}
					// Shouldn't reach here in well-formed tests
					w.WriteHeader(500)
					_, _ = w.Write([]byte("unexpected poll"))
					return
				}
				step := tt.pollResponses[pollIndex]
				pollIndex++
				if step.retryAfter != "" {
					w.Header().Set("Retry-After", step.retryAfter)
				}
				w.WriteHeader(step.status)
				_, _ = w.Write([]byte(step.body))
			}))
			defer srv.Close()

			// Build a fake 202 response whose Request.URL points at our test server
			// so the poll URL derivation works.
			reqURL, _ := url.Parse(srv.URL + "/invocations?api-version=test")
			resp := &http.Response{
				StatusCode: http.StatusAccepted,
				Status:     "202 Accepted",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(tt.initial202Body)),
				Request:    &http.Request{URL: reqURL},
			}
			if tt.initial202Header != "" {
				resp.Header.Set("x-agent-invocation-id", tt.initial202Header)
			}

			err := handleInvocationLRO(t.Context(), resp, "", "", "test-agent", tt.timeout, "", nil, false)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHandleInvocationLRO_PropagatesUserIdentityHeader(t *testing.T) {
	pollRequests := captureInvocationLROPollRequests(
		t,
		&agent_api.SessionRequestOptions{
			UserIdentity: "user-1",
		},
		`{"status":"running"}`,
		`{"status":"completed"}`,
	)
	if len(pollRequests) < 2 {
		t.Fatalf("poll request count = %d, want at least 2", len(pollRequests))
	}
	assertPollRequestsHaveHeaders(t, pollRequests, "user-1")
}

func TestHandleInvocationLRO_PropagatesPartialUserIdentityHeader(t *testing.T) {
	pollRequests := captureInvocationLROPollRequests(
		t,
		&agent_api.SessionRequestOptions{
			UserIdentity: "user-1",
		},
	)
	assertPollRequestsHaveHeaders(t, pollRequests, "user-1")
}

func captureInvocationLROPollRequests(
	t *testing.T,
	options *agent_api.SessionRequestOptions,
	pollBodies ...string,
) []*http.Request {
	t.Helper()

	origInterval := defaultLROPollInterval
	defaultLROPollInterval = time.Millisecond
	t.Cleanup(func() { defaultLROPollInterval = origInterval })

	// Use two-poll scenario: first poll returns "running", second returns "completed".
	// Verify that the user-identity header is sent on every poll, not just the first.
	if len(pollBodies) == 0 {
		pollBodies = []string{
			`{"status":"running"}`,
			`{"status":"completed"}`,
		}
	}

	numPolls := len(pollBodies)
	pollReqCh := make(chan *http.Request, numPolls)
	respQueue := make(chan string, numPolls)
	for _, body := range pollBodies {
		respQueue <- body
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollReqCh <- r
		body := <-respQueue
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	reqURL, _ := url.Parse(srv.URL + "/invocations?api-version=test")
	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Status:     "202 Accepted",
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"status":"accepted"}`)),
		Request:    &http.Request{URL: reqURL},
	}
	resp.Header.Set("x-agent-invocation-id", "inv-headers")

	err := handleInvocationLRO(
		t.Context(),
		resp,
		"",
		"token",
		"test-agent",
		time.Second,
		"",
		options,
		false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	close(pollReqCh)
	pollRequests := make([]*http.Request, 0, numPolls)
	for r := range pollReqCh {
		pollRequests = append(pollRequests, r)
	}
	if len(pollRequests) != numPolls {
		t.Errorf("expected %d poll requests, got %d", numPolls, len(pollRequests))
	}

	return pollRequests
}

func assertPollRequestsHaveHeaders(
	t *testing.T,
	pollRequests []*http.Request,
	wantUser string,
) {
	t.Helper()

	for i, r := range pollRequests {
		pollNumber := i + 1
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("poll %d: Authorization = %q, want Bearer token", pollNumber, got)
		}
		if got := r.Header.Get(agent_api.UserIdentityHeader); got != wantUser {
			t.Errorf("poll %d: %s = %q, want %q", pollNumber, agent_api.UserIdentityHeader, got, wantUser)
		}
	}
}

type pollStep struct {
	status     int
	body       string
	retryAfter string
	repeat     bool
}

func TestCreateConversation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		agentName  string
		statusCode int
		body       string
		wantID     string
		wantErr    bool
		errContain string
	}{
		{
			name:       "success returns conversation ID",
			agentName:  "my-agent",
			statusCode: 200,
			body:       `{"id":"conv-abc123"}`,
			wantID:     "conv-abc123",
		},
		{
			name:       "HTTP 400 returns error",
			agentName:  "my-agent",
			statusCode: 400,
			body:       `{"error":"bad request"}`,
			wantErr:    true,
			errContain: "failed with HTTP 400",
		},
		{
			name:       "HTTP 500 returns error",
			agentName:  "my-agent",
			statusCode: 500,
			body:       `{"error":"internal"}`,
			wantErr:    true,
			errContain: "failed with HTTP 500",
		},
		{
			name:       "response missing id field",
			agentName:  "my-agent",
			statusCode: 200,
			body:       `{"status":"ok"}`,
			wantErr:    true,
			errContain: "missing 'id' field",
		},
		{
			name:       "response with non-string id",
			agentName:  "my-agent",
			statusCode: 200,
			body:       `{"id":12345}`,
			wantErr:    true,
			errContain: "missing 'id' field",
		},
		{
			name:       "invalid JSON response",
			agentName:  "my-agent",
			statusCode: 200,
			body:       `not-json`,
			wantErr:    true,
			errContain: "invalid character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request method
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}

				// Verify path includes the agent name and conversations endpoint
				wantPath := "/agents/" + tt.agentName +
					"/endpoint/protocols/openai/conversations"
				if r.URL.Path != wantPath {
					t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
				}

				// The api-version must travel as a query parameter, not in the route.
				if got := r.URL.Query().Get("api-version"); got != "v1" {
					t.Errorf("api-version = %q, want %q", got, "v1")
				}

				// Verify auth header
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
				}

				// Verify content type
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want %q", got, "application/json")
				}

				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			id, err := createConversation(t.Context(), srv.URL, tt.agentName, "test-token", "v1", nil)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContain != "" &&
					!strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("error = %q, want substring %q",
						err.Error(), tt.errContain)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.wantID {
				t.Errorf("id = %q, want %q", id, tt.wantID)
			}
		})
	}
}

func TestCreateConversation_PropagatesUserIdentityHeader(t *testing.T) {
	t.Parallel()

	reqCh := make(chan *http.Request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCh <- r
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"conv-headers"}`))
	}))
	defer srv.Close()

	id, err := createConversation(
		t.Context(),
		srv.URL,
		"my-agent",
		"test-token",
		"v1",
		&agent_api.SessionRequestOptions{
			UserIdentity: "user-1",
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "conv-headers" {
		t.Fatalf("id = %q, want conv-headers", id)
	}

	request := <-reqCh
	if got := request.Header.Get(agent_api.UserIdentityHeader); got != "user-1" {
		t.Errorf("%s = %q, want user-1", agent_api.UserIdentityHeader, got)
	}
}

func TestResponseTraceID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name:    "prefers x-request-id when both present",
			headers: map[string]string{"X-Request-ID": "trace-abc", "apim-request-id": "apim-xyz"},
			want:    "trace-abc",
		},
		{
			name:    "falls back to apim-request-id",
			headers: map[string]string{"apim-request-id": "apim-xyz"},
			want:    "apim-xyz",
		},
		{
			name:    "returns empty when neither present",
			headers: map[string]string{},
			want:    "",
		},
		{
			name:    "returns x-request-id when only it is present",
			headers: map[string]string{"X-Request-ID": "trace-only"},
			want:    "trace-only",
		},
		{
			name:    "deduplicates comma-folded x-request-id",
			headers: map[string]string{"X-Request-ID": "trace-abc,trace-abc"},
			want:    "trace-abc",
		},
		{
			name:    "returns first token when x-request-id is comma-list",
			headers: map[string]string{"X-Request-ID": "trace-first, trace-second"},
			want:    "trace-first",
		},
		{
			name:    "skips leading empty token in comma-folded x-request-id",
			headers: map[string]string{"X-Request-ID": ", trace-second"},
			want:    "trace-second",
		},
		{
			name:    "deduplicates comma-folded apim-request-id fallback",
			headers: map[string]string{"apim-request-id": "apim-xyz, apim-xyz"},
			want:    "apim-xyz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{Header: http.Header{}}
			for k, v := range tt.headers {
				resp.Header.Set(k, v)
			}

			if got := responseTraceID(resp); got != tt.want {
				t.Errorf("responseTraceID() = %q, want %q", got, tt.want)
			}
		})
	}
}
