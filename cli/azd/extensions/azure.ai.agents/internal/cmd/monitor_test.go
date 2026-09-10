// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMonitorCommand_AcceptsPositionalArg(t *testing.T) {
	cmd := newMonitorCommand(nil)
	err := cmd.Args(cmd, []string{"my-agent"})
	assert.NoError(t, err)
}

func TestMonitorCommand_AcceptsNoArgs(t *testing.T) {
	cmd := newMonitorCommand(nil)
	err := cmd.Args(cmd, []string{})
	assert.NoError(t, err)
}

func TestMonitorCommand_RejectsMultipleArgs(t *testing.T) {
	cmd := newMonitorCommand(nil)
	err := cmd.Args(cmd, []string{"svc1", "svc2"})
	assert.Error(t, err)
}

func TestValidateMonitorFlags_Valid(t *testing.T) {
	flags := &monitorFlags{
		tail:    50,
		logType: "console",
	}
	err := validateMonitorFlags(flags)
	assert.NoError(t, err)
}

func TestValidateMonitorFlags_ValidSystem(t *testing.T) {
	flags := &monitorFlags{
		tail:    100,
		logType: "system",
	}
	err := validateMonitorFlags(flags)
	assert.NoError(t, err)
}

func TestValidateMonitorFlags_TailTooLow(t *testing.T) {
	flags := &monitorFlags{
		tail:    0,
		logType: "console",
	}
	err := validateMonitorFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--tail must be between 1 and 300")
}

func TestValidateMonitorFlags_TailTooHigh(t *testing.T) {
	flags := &monitorFlags{
		tail:    301,
		logType: "console",
	}
	err := validateMonitorFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--tail must be between 1 and 300")
}

func TestValidateMonitorFlags_TailBoundary(t *testing.T) {
	flags := &monitorFlags{tail: 1, logType: "console"}
	assert.NoError(t, validateMonitorFlags(flags))

	flags = &monitorFlags{tail: 300, logType: "console"}
	assert.NoError(t, validateMonitorFlags(flags))
}

func TestValidateMonitorFlags_InvalidType(t *testing.T) {
	flags := &monitorFlags{
		tail:    50,
		logType: "invalid",
	}
	err := validateMonitorFlags(flags)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--type must be 'console' or 'system'")
}

func TestMonitorCommand_DefaultValues(t *testing.T) {
	cmd := newMonitorCommand(nil)

	// Verify default flag values
	tail, _ := cmd.Flags().GetInt("tail")
	assert.Equal(t, 50, tail)

	logType, _ := cmd.Flags().GetString("type")
	assert.Equal(t, "console", logType)

	follow, _ := cmd.Flags().GetBool("follow")
	assert.Equal(t, false, follow)

	session, _ := cmd.Flags().GetString("session-id")
	assert.Equal(t, "", session)

	userIdentity, _ := cmd.Flags().GetString("user-identity")
	assert.Equal(t, "", userIdentity)
}

func TestMonitorCommand_SessionFlagRegistered(t *testing.T) {
	cmd := newMonitorCommand(nil)

	// The --session-id / -s flag must be defined
	f := cmd.Flags().Lookup("session-id")
	require.NotNil(t, f, "--session-id flag should be registered")
	assert.Equal(t, "s", f.Shorthand)
}

func TestResolveMonitorAgentInfo_PrefersProjectService(t *testing.T) {
	t.Parallel()

	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"service-key": {
				Name: "service-key",
				Host: AiAgentHost,
			},
		},
	}}
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values: map[string]map[string]string{"test": {
			"AGENT_SERVICE_KEY_NAME":    "deployed-agent",
			"AGENT_SERVICE_KEY_VERSION": "7",
			"AGENT_SERVICE_KEY_ENDPOINT": "https://account.services.ai.azure.com/api/projects/project/" +
				"agents/deployed-agent/versions/7",
		}},
	}
	azdClient := newHelpersTestAzdClient(
		t, projectServer, &helpersPromptServer{}, envServer,
	)

	info, err := resolveMonitorAgentInfo(t.Context(), azdClient, "service-key", true)
	require.NoError(t, err)
	assert.Equal(t, "service-key", info.ServiceName)
	assert.Equal(t, "deployed-agent", info.AgentName)
	assert.Equal(t, "7", info.Version)
	assert.Contains(t, info.AgentEndpoint, "/agents/deployed-agent/versions/7")
}

func TestResolveMonitorAgentInfo_UsesDirectNameWithoutProject(t *testing.T) {
	t.Parallel()

	projectServer := &helpersProjectServer{
		err: status.Error(codes.Unknown, "no project exists; to create a new project, run `azd init`"),
	}
	azdClient := newHelpersTestAzdClient(t, projectServer, &helpersPromptServer{})

	info, err := resolveMonitorAgentInfo(t.Context(), azdClient, "hosted-agent", true)
	require.NoError(t, err)
	assert.Empty(t, info.ServiceName)
	assert.Equal(t, "hosted-agent", info.AgentName)
}

func TestResolveMonitorAgentInfo_DoesNotUseDirectNameInsideProject(t *testing.T) {
	t.Parallel()

	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"service-key": {
				Name: "service-key",
				Host: AiAgentHost,
			},
		},
	}}
	azdClient := newHelpersTestAzdClient(t, projectServer, &helpersPromptServer{})

	_, err := resolveMonitorAgentInfo(t.Context(), azdClient, "hosted-agent", true)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no azure.ai.agent service named 'hosted-agent'")
}

func TestResolveMonitorAgentInfo_RequiresNameWithoutProject(t *testing.T) {
	t.Parallel()

	projectServer := &helpersProjectServer{
		err: status.Error(codes.Unknown, "no project exists; to create a new project, run `azd init`"),
	}
	azdClient := newHelpersTestAzdClient(t, projectServer, &helpersPromptServer{})

	_, err := resolveMonitorAgentInfo(t.Context(), azdClient, "", true)
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidAgentName, localErr.Code)
	assert.Equal(t, "agent name is required outside an azd project", localErr.Message)
	assert.Contains(t, localErr.Suggestion, "<agent-name>")
	assert.Contains(t, localErr.Suggestion, "run from an azd project")
}

func TestResolveMonitorAgentInfo_DoesNotHideProjectTransportFailure(t *testing.T) {
	t.Parallel()

	projectServer := &helpersProjectServer{
		err: status.Error(codes.Unavailable, "project service unavailable"),
	}
	azdClient := newHelpersTestAzdClient(t, projectServer, &helpersPromptServer{})

	_, err := resolveMonitorAgentInfo(t.Context(), azdClient, "hosted-agent", true)
	require.Error(t, err)
	assert.ErrorContains(t, err, "project service unavailable")
}

func TestIsMissingAzdProjectError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "current host error",
			err:  status.Error(codes.Unknown, "no project exists; run `azd init`"),
			want: true,
		},
		{
			name: "future not found code",
			err:  status.Error(codes.NotFound, "project not found"),
			want: true,
		},
		{
			name: "unrelated not found",
			err:  status.Error(codes.NotFound, "service not found"),
			want: false,
		},
		{
			name: "transport failure",
			err:  status.Error(codes.Unavailable, "connection refused"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isMissingAzdProjectError(tt.err))
		})
	}
}

func TestMonitorAgentKey(t *testing.T) {
	t.Parallel()

	projectEndpoint := "https://account.services.ai.azure.com/api/projects/project"

	t.Run("uses project endpoint for direct hosted name", func(t *testing.T) {
		t.Parallel()

		info := &AgentServiceInfo{AgentName: "hosted-agent"}
		assert.Equal(
			t,
			buildAgentKey(projectEndpoint, "hosted-agent", "", false),
			monitorAgentKey(info, projectEndpoint),
		)
	})

	t.Run("prefers deployed agent endpoint", func(t *testing.T) {
		t.Parallel()

		agentEndpoint := projectEndpoint + "/agents/deployed-agent/versions/7"
		info := &AgentServiceInfo{
			AgentName:     "deployed-agent",
			Version:       "7",
			AgentEndpoint: agentEndpoint,
		}
		assert.Equal(
			t,
			buildRemoteAgentKeyFromEndpoint(agentEndpoint),
			monitorAgentKey(info, "https://different.example.com/api/projects/other"),
		)
	})
}

func TestMonitorEndpointError_AddsOutsideProjectGuidance(t *testing.T) {
	t.Parallel()

	err := monitorEndpointError(noProjectEndpointError())
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
	assert.Equal(t, "no Foundry project endpoint is configured", localErr.Message)
	assert.Contains(t, localErr.Suggestion, "azd ai project set")
	assert.Contains(t, localErr.Suggestion, "azure.yaml")
}

func TestResolveMonitorSession_UsesDirectAgentKey(t *testing.T) {
	t.Parallel()

	userConfig := newInvokeUserConfigServer()
	azdClient := newInvokeTestAzdClient(t, userConfig)
	agentKey := buildAgentKey(
		"https://account.services.ai.azure.com/api/projects/project",
		"hosted-agent",
		"",
		false,
	)
	userConfig.setJSON(t, configPath("sessions"), map[string]string{
		agentKey: "persisted-session",
	})

	sessionID := resolveMonitorSession(
		t.Context(),
		azdClient,
		agentKey,
		legacyKeysForRemote("hosted-agent")...,
	)
	assert.Equal(t, "persisted-session", sessionID)
}

func TestMonitorCommand_FollowFlagRegistered(t *testing.T) {
	cmd := newMonitorCommand(nil)

	f := cmd.Flags().Lookup("follow")
	require.NotNil(t, f, "--follow flag should be registered")
	assert.Equal(t, "f", f.Shorthand)
}

func TestValidateMonitorFlags_SessionBypassesTailAndType(t *testing.T) {
	// When a session ID is set, tail and logType are irrelevant (used only for container logstream).
	// Validation should still pass with valid defaults even when session is set.
	flags := &monitorFlags{
		sessionID: "some-session-id",
		tail:      50,
		logType:   "console",
	}
	err := validateMonitorFlags(flags)
	assert.NoError(t, err)
}

func TestValidateMonitorFlags_TableDriven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   monitorFlags
		wantErr string
	}{
		{
			name:    "valid console defaults",
			flags:   monitorFlags{tail: 50, logType: "console"},
			wantErr: "",
		},
		{
			name:    "valid system log type",
			flags:   monitorFlags{tail: 1, logType: "system"},
			wantErr: "",
		},
		{
			name:    "tail at max boundary",
			flags:   monitorFlags{tail: 300, logType: "console"},
			wantErr: "",
		},
		{
			name:    "tail zero",
			flags:   monitorFlags{tail: 0, logType: "console"},
			wantErr: "--tail must be between 1 and 300",
		},
		{
			name:    "tail negative",
			flags:   monitorFlags{tail: -1, logType: "console"},
			wantErr: "--tail must be between 1 and 300",
		},
		{
			name:    "tail exceeds max",
			flags:   monitorFlags{tail: 301, logType: "console"},
			wantErr: "--tail must be between 1 and 300",
		},
		{
			name:    "invalid log type",
			flags:   monitorFlags{tail: 50, logType: "debug"},
			wantErr: "--type must be 'console' or 'system'",
		},
		{
			name:    "empty log type",
			flags:   monitorFlags{tail: 50, logType: ""},
			wantErr: "--type must be 'console' or 'system'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMonitorFlags(&tt.flags)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestLoadLocalContext_WithSessions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigFile)

	ctx := &AgentLocalContext{
		AgentName: "my-agent",
		Sessions: map[string]string{
			"agent-a": "session-123",
			"agent-b": "session-456",
		},
	}
	data, err := json.MarshalIndent(ctx, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0600))

	loaded := loadLocalContext(configPath)
	assert.Equal(t, "my-agent", loaded.AgentName)
	assert.Equal(t, "session-123", loaded.Sessions["agent-a"])
	assert.Equal(t, "session-456", loaded.Sessions["agent-b"])
}

func TestLoadLocalContext_MissingFile(t *testing.T) {
	t.Parallel()

	loaded := loadLocalContext(filepath.Join(t.TempDir(), "nonexistent.json"))
	assert.NotNil(t, loaded)
	assert.Nil(t, loaded.Sessions)
}

func TestLoadLocalContext_InvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigFile)
	require.NoError(t, os.WriteFile(configPath, []byte("{invalid json"), 0600))

	loaded := loadLocalContext(configPath)
	assert.NotNil(t, loaded)
	assert.Nil(t, loaded.Sessions)
}

func TestLoadLocalContext_EmptyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigFile)
	require.NoError(t, os.WriteFile(configPath, []byte("{}"), 0600))

	loaded := loadLocalContext(configPath)
	assert.NotNil(t, loaded)
	assert.Empty(t, loaded.AgentName)
	assert.Nil(t, loaded.Sessions)
}

func TestBuildAgentKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		agent    string
		version  string
		local    bool
		want     string
	}{
		{
			name:     "remote with version",
			endpoint: "https://myaccount.services.ai.azure.com/api/projects/myproject",
			agent:    "my-agent",
			version:  "3",
			local:    false,
			want:     "myaccount.services.ai.azure.com/api/projects/myproject/agents/my-agent/versions/3/remote",
		},
		{
			name:     "remote without version defaults to latest",
			endpoint: "https://myaccount.services.ai.azure.com/api/projects/myproject",
			agent:    "my-agent",
			version:  "",
			local:    false,
			want:     "myaccount.services.ai.azure.com/api/projects/myproject/agents/my-agent/versions/latest/remote",
		},
		{
			name:     "local mode",
			endpoint: "localhost:8088",
			agent:    "test-agent",
			version:  "latest",
			local:    true,
			want:     "localhost:8088/agents/test-agent/versions/latest/local",
		},
		{
			name:     "trailing slash trimmed from endpoint",
			endpoint: "https://example.com/",
			agent:    "agent",
			version:  "1",
			local:    false,
			want:     "example.com/agents/agent/versions/1/remote",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildAgentKey(tt.endpoint, tt.agent, tt.version, tt.local)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"https://Example.COM/path/", "example.com/path"},
		{"https://example.com/path", "example.com/path"},
		{"HTTP://HOST.COM/", "host.com"},
		{"localhost:8088", "localhost:8088"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, normalizeEndpoint(tt.input))
		})
	}
}

func TestProjectHash(t *testing.T) {
	t.Parallel()

	// Same path produces same hash
	h1 := projectHash("/some/path")
	h2 := projectHash("/some/path")
	assert.Equal(t, h1, h2)

	// Different paths produce different hashes
	h3 := projectHash("/other/path")
	assert.NotEqual(t, h1, h3)

	// Hash is 16 hex chars (8 bytes)
	assert.Len(t, h1, 16)
}

func TestLegacyKeysForRemote(t *testing.T) {
	t.Parallel()

	keys := legacyKeysForRemote("my-agent")
	assert.Contains(t, keys, "my-agent")
}

func TestLegacyKeysForLocal(t *testing.T) {
	t.Parallel()

	keys := legacyKeysForLocal("my-service", "my-agent")
	assert.Contains(t, keys, "my-service-local")
	assert.Contains(t, keys, "my-agent-local")
	assert.Contains(t, keys, "local")
}

func TestResolveStoredIDFromPath_ExplicitID(t *testing.T) {
	t.Parallel()

	got, err := resolveStoredIDFromPath("", "agent-key", "explicit-id", false, "sessions", false)
	require.NoError(t, err)
	assert.Equal(t, "explicit-id", got)
}

func TestResolveStoredIDFromPath_GenerateWhenMissing(t *testing.T) {
	t.Parallel()

	got, err := resolveStoredIDFromPath("", "agent-key", "", false, "sessions", true)
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	// Should be a UUID format
	assert.Len(t, got, 36)
}

func TestResolveStoredIDFromPath_EmptyWhenNotGenerating(t *testing.T) {
	t.Parallel()

	got, err := resolveStoredIDFromPath("", "agent-key", "", false, "sessions", false)
	require.NoError(t, err)
	assert.Empty(t, got)
}
