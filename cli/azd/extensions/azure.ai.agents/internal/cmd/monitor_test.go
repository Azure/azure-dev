// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}

func TestMonitorCommand_SessionFlagRegistered(t *testing.T) {
	cmd := newMonitorCommand(nil)

	// The --session-id / -s flag must be defined
	f := cmd.Flags().Lookup("session-id")
	require.NotNil(t, f, "--session-id flag should be registered")
	assert.Equal(t, "s", f.Shorthand)
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
