// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProjectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	testAgentEndpoint   = testProjectEndpoint + "/agents/support/endpoint/protocols/invocations"
)

func newDirectTargetTestCommand(t *testing.T, args ...string) (*cobra.Command, *directAgentTargetFlags) {
	t.Helper()
	flags := &directAgentTargetFlags{}
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("environment", "", "")
	addDirectAgentTargetFlags(cmd, flags, "--agent-name")
	require.NoError(t, cmd.ParseFlags(args))
	return cmd, flags
}

func requireLocalErrorCode(t *testing.T, err error, code string) *azdext.LocalError {
	t.Helper()
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected LocalError, got %T: %v", err, err)
	assert.Equal(t, code, localErr.Code)
	return localErr
}

func TestResolveDirectAgentTarget(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		agentName    string
		wantNil      bool
		wantEndpoint string
		wantAgent    string
		wantCode     string
		// nameSupplied marks the name selector as explicitly supplied even when empty.
		nameSupplied bool
	}{
		{name: "project mode when no direct flag", agentName: "local-service", wantNil: true},
		{
			name:         "agent endpoint",
			args:         []string{"--agent-endpoint", testAgentEndpoint},
			wantEndpoint: testProjectEndpoint,
			wantAgent:    "support",
		},
		{
			name: "responses agent endpoint",
			args: []string{"--agent-endpoint",
				testProjectEndpoint + "/agents/support/endpoint/protocols/openai/responses?api-version=v1"},
			wantEndpoint: testProjectEndpoint,
			wantAgent:    "support",
		},
		{
			name:         "project endpoint uses deployed name verbatim",
			args:         []string{"--project-endpoint", testProjectEndpoint + "/"},
			agentName:    "support-prod",
			wantEndpoint: testProjectEndpoint,
			wantAgent:    "support-prod",
		},
		{
			name:      "agent endpoint conflicts with name",
			args:      []string{"--agent-endpoint", testAgentEndpoint},
			agentName: "support",
			wantCode:  exterrors.CodeConflictingArguments,
		},
		{
			name:         "agent endpoint conflicts with explicitly empty name",
			args:         []string{"--agent-endpoint", testAgentEndpoint},
			nameSupplied: true,
			wantCode:     exterrors.CodeConflictingArguments,
		},
		{
			name:         "project endpoint rejects explicitly empty name",
			args:         []string{"--project-endpoint", testProjectEndpoint},
			nameSupplied: true,
			wantCode:     exterrors.CodeInvalidAgentName,
		},
		{
			name: "both endpoints conflict",
			args: []string{"--agent-endpoint", testAgentEndpoint,
				"--project-endpoint", testProjectEndpoint},
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name:      "environment conflicts with direct target",
			args:      []string{"--project-endpoint", testProjectEndpoint, "--environment", "dev"},
			agentName: "support",
			wantCode:  exterrors.CodeConflictingArguments,
		},
		{
			name:     "project endpoint requires name",
			args:     []string{"--project-endpoint", testProjectEndpoint},
			wantCode: exterrors.CodeInvalidAgentName,
		},
		{
			name:      "invalid deployed name",
			args:      []string{"--project-endpoint", testProjectEndpoint},
			agentName: "bad_name",
			wantCode:  exterrors.CodeInvalidAgentName,
		},
		{
			name:      "explicitly empty project endpoint",
			args:      []string{"--project-endpoint", ""},
			agentName: "support",
			wantCode:  exterrors.CodeInvalidParameter,
		},
		{
			name:     "explicitly empty agent endpoint",
			args:     []string{"--agent-endpoint", ""},
			wantCode: exterrors.CodeInvalidParameter,
		},
		{
			name:      "project endpoint without project path",
			args:      []string{"--project-endpoint", "https://acct.services.ai.azure.com"},
			agentName: "support",
			wantCode:  exterrors.CodeInvalidParameter,
		},
		{
			name:      "project endpoint with nested path",
			args:      []string{"--project-endpoint", testProjectEndpoint + "/agents/support"},
			agentName: "support",
			wantCode:  exterrors.CodeInvalidParameter,
		},
		{
			name:      "non Foundry project endpoint",
			args:      []string{"--project-endpoint", "https://example.com/api/projects/proj"},
			agentName: "support",
			wantCode:  exterrors.CodeInvalidParameter,
		},
		{
			name: "websocket agent endpoint",
			args: []string{"--agent-endpoint",
				"wss://acct.services.ai.azure.com/api/projects/proj/agents/support/endpoint/protocols/invocations"},
			wantCode: exterrors.CodeInvalidParameter,
		},
		{
			name:     "agent endpoint without protocol path",
			args:     []string{"--agent-endpoint", testProjectEndpoint + "/agents/support"},
			wantCode: exterrors.CodeInvalidParameter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, flags := newDirectTargetTestCommand(t, tt.args...)
			target, err := resolveDirectAgentTarget(
				cmd, flags, tt.agentName, tt.nameSupplied || tt.agentName != "", "--agent-name")
			if tt.wantCode != "" {
				requireLocalErrorCode(t, err, tt.wantCode)
				assert.Nil(t, target)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, target)
				return
			}
			require.NotNil(t, target)
			assert.Equal(t, tt.wantEndpoint, target.ProjectEndpoint)
			assert.Equal(t, tt.wantAgent, target.Name)
		})
	}
}

func TestResolveDirectAgentTarget_IgnoresCurrentDirectory(t *testing.T) {
	args := []string{"--project-endpoint", testProjectEndpoint}
	cmd, flags := newDirectTargetTestCommand(t, args...)
	want, err := resolveDirectAgentTarget(cmd, flags, "support-prod", true, "--agent-name")
	require.NoError(t, err)

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "azure.yaml"), []byte(
		"name: other\nservices:\n  support:\n    host: azure.ai.agent\n"), 0o600))
	t.Chdir(projectDir)
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://other.services.ai.azure.com/api/projects/other")

	cmd, flags = newDirectTargetTestCommand(t, args...)
	got, err := resolveDirectAgentTarget(cmd, flags, "support-prod", true, "--agent-name")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestValidateDirectSessionID(t *testing.T) {
	const listCommand = "azd ai agent sessions list --agent-endpoint \"x\""
	assert.NoError(t, validateDirectSessionID("sess-123", listCommand))
	localErr := requireLocalErrorCode(t, validateDirectSessionID("", listCommand), exterrors.CodeInvalidSessionId)
	assert.Contains(t, localErr.Suggestion, listCommand)
	for _, sessionID := range []string{"", "  ", "../other", "a/b", "a?b", " sess"} {
		t.Run(sessionID, func(t *testing.T) {
			requireLocalErrorCode(t, validateDirectSessionID(sessionID, listCommand), exterrors.CodeInvalidSessionId)
		})
	}
}

func TestDirectSessionsListCommand(t *testing.T) {
	cmd, flags := newDirectTargetTestCommand(t, "--project-endpoint", testProjectEndpoint+"/")
	target, err := resolveDirectAgentTarget(cmd, flags, "support-prod", true, "--agent-name")
	require.NoError(t, err)
	assert.Equal(t,
		`azd ai agent sessions list --agent-name support-prod --project-endpoint "`+testProjectEndpoint+`"`,
		directSessionsListCommand(flags, target))

	cmd, flags = newDirectTargetTestCommand(t, "--agent-endpoint", testAgentEndpoint)
	target, err = resolveDirectAgentTarget(cmd, flags, "", false, "--agent-name")
	require.NoError(t, err)
	assert.Equal(t, `azd ai agent sessions list --agent-endpoint "`+testAgentEndpoint+`"`,
		directSessionsListCommand(flags, target))
}

type fakeRemoteAgentReader struct {
	agent    *agent_api.AgentObject
	err      error
	gotName  string
	gotCalls int
}

func (f *fakeRemoteAgentReader) GetAgent(
	_ context.Context, name, _ string, _ bool,
) (*agent_api.AgentObject, error) {
	f.gotName = name
	f.gotCalls++
	return f.agent, f.err
}

func agentWithKind(name, kind string) *agent_api.AgentObject {
	agent := &agent_api.AgentObject{Name: name}
	agent.Versions.Latest = agent_api.AgentVersionObject{
		Name: name, Version: "3", Status: "active",
	}
	if kind != "" {
		agent.Versions.Latest.Definition = map[string]any{"kind": kind}
	}
	return agent
}

func newTestResponseError(t *testing.T, status int) error {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, testAgentEndpoint, nil)
	return runtime.NewResponseError(&http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"Denied","message":"denied"}}`)),
		Request:    req,
	})
}

func TestPrepareDirectFilesContext(t *testing.T) {
	target := &AgentContext{ProjectEndpoint: testProjectEndpoint, Name: "support-prod"}

	t.Run("hosted agent", func(t *testing.T) {
		reader := &fakeRemoteAgentReader{agent: agentWithKind("support-prod", "hosted")}
		fc, err := prepareDirectFilesContext(t.Context(), reader, target, "sess-1")
		require.NoError(t, err)
		assert.Equal(t, "support-prod", reader.gotName)
		assert.Equal(t, target, fc.AgentContext)
		assert.Equal(t, "sess-1", fc.sessionID)
	})

	t.Run("undeclared kind is left to the service", func(t *testing.T) {
		reader := &fakeRemoteAgentReader{agent: agentWithKind("support-prod", "")}
		_, err := prepareDirectFilesContext(t.Context(), reader, target, "sess-1")
		require.NoError(t, err)
	})

	for _, kind := range []string{"prompt", "workflow"} {
		t.Run("rejects "+kind, func(t *testing.T) {
			reader := &fakeRemoteAgentReader{agent: agentWithKind("support-prod", kind)}
			_, err := prepareDirectFilesContext(t.Context(), reader, target, "sess-1")
			localErr := requireLocalErrorCode(t, err, exterrors.CodeUnsupportedAgentKind)
			assert.Contains(t, localErr.Message, kind)
		})
	}

	t.Run("agent not found", func(t *testing.T) {
		reader := &fakeRemoteAgentReader{err: newTestResponseError(t, http.StatusNotFound)}
		_, err := prepareDirectFilesContext(t.Context(), reader, target, "sess-1")
		localErr := requireLocalErrorCode(t, err, exterrors.CodeAgentNotFound)
		assert.Contains(t, localErr.Message, "support-prod")
	})

	t.Run("agent access denied", func(t *testing.T) {
		reader := &fakeRemoteAgentReader{err: newTestResponseError(t, http.StatusForbidden)}
		_, err := prepareDirectFilesContext(t.Context(), reader, target, "sess-1")
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok, "expected ServiceError, got %T", err)
		assert.Equal(t, http.StatusForbidden, serviceErr.StatusCode)
		assert.Contains(t, serviceErr.ErrorCode, exterrors.OpGetAgent)
		assert.NotEmpty(t, serviceErr.Suggestion)
	})
}

func TestClassifySessionFileError(t *testing.T) {
	t.Run("missing session", func(t *testing.T) {
		err := classifySessionFileError(newTestResponseError(t, http.StatusNotFound),
			exterrors.OpListSessionFiles, "support", "sess-1", "/data", "")
		localErr := requireLocalErrorCode(t, err, exterrors.CodeSessionNotFound)
		assert.Contains(t, localErr.Message, "sess-1")
		assert.Contains(t, localErr.Message, "/data")
		assert.Contains(t, localErr.Suggestion, "`azd ai agent sessions list`")
	})

	t.Run("missing session suggests direct list command", func(t *testing.T) {
		const listCommand = `azd ai agent sessions list --agent-endpoint "x"`
		err := classifySessionFileError(newTestResponseError(t, http.StatusNotFound),
			exterrors.OpGetSession, "support", "sess-1", "", listCommand)
		localErr := requireLocalErrorCode(t, err, exterrors.CodeSessionNotFound)
		assert.Contains(t, localErr.Suggestion, listCommand)
	})

	t.Run("denied session keeps status and identity hint", func(t *testing.T) {
		err := classifySessionFileError(newTestResponseError(t, http.StatusForbidden),
			exterrors.OpDownloadSessionFile, "support", "sess-1", "/data/out.csv", "")
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok, "expected ServiceError, got %T", err)
		assert.Equal(t, http.StatusForbidden, serviceErr.StatusCode)
		assert.Contains(t, serviceErr.ErrorCode, exterrors.OpDownloadSessionFile)
		assert.Contains(t, serviceErr.Suggestion, "--user-identity")
	})

	t.Run("other service failure", func(t *testing.T) {
		err := classifySessionFileError(newTestResponseError(t, http.StatusInternalServerError),
			exterrors.OpListSessionFiles, "support", "sess-1", "", "")
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok, "expected ServiceError, got %T", err)
		assert.Equal(t, http.StatusInternalServerError, serviceErr.StatusCode)
	})
}

func TestShowDirectAgent_PrintsLatestVersion(t *testing.T) {
	reader := &fakeRemoteAgentReader{agent: agentWithKind("support-prod", "hosted")}
	target := &AgentContext{ProjectEndpoint: testProjectEndpoint, Name: "support-prod"}

	output, err := captureStdout(t, func() error {
		return showDirectAgent(t.Context(), reader, target, "json")
	})
	require.NoError(t, err)
	assert.Equal(t, "support-prod", reader.gotName)
	assert.Contains(t, output, `"name": "support-prod"`)
	assert.Contains(t, output, `"version": "3"`)
	assert.NotContains(t, output, "playground_url")
	assert.NotContains(t, output, "next_step")
}

func TestShowDirectAgent_NotFound(t *testing.T) {
	reader := &fakeRemoteAgentReader{err: newTestResponseError(t, http.StatusNotFound)}
	target := &AgentContext{ProjectEndpoint: testProjectEndpoint, Name: "missing"}
	err := showDirectAgent(t.Context(), reader, target, "json")
	requireLocalErrorCode(t, err, exterrors.CodeAgentNotFound)
}

// executeUnderRoot runs sub beneath a root that owns the reserved --environment flag.
func executeUnderRoot(sub *cobra.Command, args ...string) error {
	root := &cobra.Command{Use: "agent", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringP("environment", "e", "", "")
	root.AddCommand(sub)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{sub.Name()}, args...))
	return root.Execute()
}

// These cases fail during local validation, before any azd host or Azure call.
func TestDirectTargetCommands_RejectInvalidInputsBeforeAuth(t *testing.T) {
	tests := []struct {
		name     string
		cmd      func() *cobra.Command
		args     []string
		wantCode string
	}{
		{
			name:     "show endpoint with positional name",
			cmd:      func() *cobra.Command { return newShowCommand(nil) },
			args:     []string{"support", "--agent-endpoint", testAgentEndpoint},
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name:     "show project endpoint without name",
			cmd:      func() *cobra.Command { return newShowCommand(nil) },
			args:     []string{"--project-endpoint", testProjectEndpoint},
			wantCode: exterrors.CodeInvalidAgentName,
		},
		{
			name: "show direct target with environment",
			cmd:  func() *cobra.Command { return newShowCommand(nil) },
			args: []string{"support", "--project-endpoint", testProjectEndpoint,
				"--environment", "dev"},
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name: "files list requires session in direct mode",
			cmd:  func() *cobra.Command { return newFilesListCommand(nil) },
			args: []string{"/data", "--agent-name", "support",
				"--project-endpoint", testProjectEndpoint},
			wantCode: exterrors.CodeInvalidSessionId,
		},
		{
			name: "files list endpoint with agent name",
			cmd:  func() *cobra.Command { return newFilesListCommand(nil) },
			args: []string{"--agent-name", "support", "--session-id", "s1",
				"--agent-endpoint", testAgentEndpoint},
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name: "files list endpoint with explicitly empty agent name",
			cmd:  func() *cobra.Command { return newFilesListCommand(nil) },
			args: []string{"--agent-name=", "--session-id", "s1",
				"--agent-endpoint", testAgentEndpoint},
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name:     "show endpoint with explicitly empty positional name",
			cmd:      func() *cobra.Command { return newShowCommand(nil) },
			args:     []string{"", "--agent-endpoint", testAgentEndpoint},
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name: "files download both endpoints",
			cmd:  func() *cobra.Command { return newFilesDownloadCommand(nil) },
			args: []string{"/data/a.csv", "--session-id", "s1", "--agent-endpoint", testAgentEndpoint,
				"--project-endpoint", testProjectEndpoint},
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name: "files download requires session in direct mode",
			cmd:  func() *cobra.Command { return newFilesDownloadCommand(nil) },
			args: []string{"/data/a.csv", "--agent-endpoint", testAgentEndpoint,
				"--session-id", ""},
			wantCode: exterrors.CodeInvalidSessionId,
		},
		{
			name:     "sessions list project endpoint without name",
			cmd:      func() *cobra.Command { return newSessionListCommand(nil) },
			args:     []string{"--project-endpoint", testProjectEndpoint},
			wantCode: exterrors.CodeInvalidAgentName,
		},
		{
			name:     "sessions list endpoint with agent name",
			cmd:      func() *cobra.Command { return newSessionListCommand(nil) },
			args:     []string{"--agent-name", "support", "--agent-endpoint", testAgentEndpoint},
			wantCode: exterrors.CodeConflictingArguments,
		},
		{
			name: "sessions show direct target with environment",
			cmd:  func() *cobra.Command { return newSessionShowCommand(nil) },
			args: []string{"s1", "--agent-endpoint", testAgentEndpoint,
				"--environment", "dev"},
			wantCode: exterrors.CodeConflictingArguments,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireLocalErrorCode(t, executeUnderRoot(tt.cmd(), tt.args...), tt.wantCode)
		})
	}
}

// The missing-session hint must name the same direct selector so it is runnable.
func TestFilesDirectMissingSession_SuggestsDirectSessionsList(t *testing.T) {
	err := executeUnderRoot(newFilesListCommand(nil),
		"/data", "--agent-name", "support", "--project-endpoint", testProjectEndpoint)
	localErr := requireLocalErrorCode(t, err, exterrors.CodeInvalidSessionId)
	assert.Contains(t, localErr.Suggestion,
		`azd ai agent sessions list --agent-name support --project-endpoint "`+testProjectEndpoint+`"`)
}

func TestClassifyDirectSessionListError(t *testing.T) {
	sc := &sessionContext{endpoint: testProjectEndpoint, agentName: "support-prod", direct: true}

	localErr := requireLocalErrorCode(t,
		classifyDirectSessionListError(newTestResponseError(t, http.StatusNotFound), sc),
		exterrors.CodeAgentNotFound)
	assert.Contains(t, localErr.Message, "support-prod")
	assert.Contains(t, localErr.Suggestion, "hosted agents")

	err := classifyDirectSessionListError(newTestResponseError(t, http.StatusForbidden), sc)
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok, "expected ServiceError, got %T", err)
	assert.Equal(t, http.StatusForbidden, serviceErr.StatusCode)
	assert.Contains(t, serviceErr.Suggestion, "--user-identity")
}

func TestResolveDirectSessionContext(t *testing.T) {
	newCmd := func(args ...string) (*cobra.Command, *directAgentTargetFlags, *string) {
		var agentName string
		cmd, direct := &cobra.Command{Use: "list"}, &directAgentTargetFlags{}
		cmd.Flags().String("environment", "", "")
		cmd.Flags().StringVarP(&agentName, "agent-name", "n", "", "")
		addDirectSessionFlags(cmd, direct)
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, direct, &agentName
	}

	cmd, direct, name := newCmd("--agent-name", "support-prod", "--project-endpoint", testProjectEndpoint)
	sc, err := resolveDirectSessionContext(cmd, direct, *name)
	require.NoError(t, err)
	require.NotNil(t, sc)
	assert.True(t, sc.direct)
	assert.Equal(t, testProjectEndpoint, sc.endpoint)
	assert.Equal(t, "support-prod", sc.agentName)
	assert.Empty(t, sc.version)
	assert.Contains(t, sc.sessionsListCommand, "--project-endpoint")

	cmd, direct, name = newCmd("--agent-name", "local-service")
	sc, err = resolveDirectSessionContext(cmd, direct, *name)
	require.NoError(t, err)
	assert.Nil(t, sc, "project mode must fall back to azure.yaml resolution")
}

func TestDirectTargetFlags_OnlyOnReadOnlyCommands(t *testing.T) {
	for _, cmd := range []*cobra.Command{
		newShowCommand(nil), newFilesListCommand(nil), newFilesDownloadCommand(nil),
		newSessionListCommand(nil), newSessionShowCommand(nil),
	} {
		assert.NotNil(t, cmd.Flags().Lookup(directAgentEndpointFlag), cmd.Name())
		assert.NotNil(t, cmd.Flags().Lookup(directProjectEndpointFlag), cmd.Name())
	}
	for _, cmd := range []*cobra.Command{
		newFilesUploadCommand(nil), newFilesRemoveCommand(nil), newFilesMkdirCommand(nil),
		newSessionCreateCommand(nil), newSessionStopCommand(nil), newSessionDeleteCommand(nil),
	} {
		assert.Nil(t, cmd.Flags().Lookup(directAgentEndpointFlag), cmd.Name())
		assert.Nil(t, cmd.Flags().Lookup(directProjectEndpointFlag), cmd.Name())
	}
}
