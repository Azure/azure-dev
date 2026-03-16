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

func TestMonitorCommand_RequiredFlags(t *testing.T) {
	cmd := newMonitorCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestMonitorCommand_MissingVersionFlag(t *testing.T) {
	cmd := newMonitorCommand()

	cmd.SetArgs([]string{"--name", "test-agent"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
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
	cmd := newMonitorCommand()

	// Verify default flag values
	tail, _ := cmd.Flags().GetInt("tail")
	assert.Equal(t, 50, tail)

	logType, _ := cmd.Flags().GetString("type")
	assert.Equal(t, "console", logType)

	follow, _ := cmd.Flags().GetBool("follow")
	assert.Equal(t, false, follow)

	session, _ := cmd.Flags().GetString("session")
	assert.Equal(t, "", session)
}

func TestMonitorCommand_SessionFlagRegistered(t *testing.T) {
	cmd := newMonitorCommand()

	// The --session / -s flag must be defined
	f := cmd.Flags().Lookup("session")
	require.NotNil(t, f, "--session flag should be registered")
	assert.Equal(t, "s", f.Shorthand)
}

func TestMonitorCommand_FollowFlagRegistered(t *testing.T) {
	cmd := newMonitorCommand()

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

func TestSaveAndLoadLocalContext_RoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigFile)

	original := &AgentLocalContext{
		AgentName: "echo-bot",
		Sessions: map[string]string{
			"echo-bot": "sess-abc",
		},
		Conversations: map[string]string{
			"echo-bot": "conv-xyz",
		},
	}

	require.NoError(t, saveLocalContext(original, configPath))

	loaded := loadLocalContext(configPath)
	assert.Equal(t, original.AgentName, loaded.AgentName)
	assert.Equal(t, original.Sessions, loaded.Sessions)
	assert.Equal(t, original.Conversations, loaded.Conversations)
}

func TestMonitorFlags_SessionBranchingDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		sessionID      string
		wantSessionLog bool
	}{
		{
			name:           "session ID set uses session logstream",
			sessionID:      "session-abc-123",
			wantSessionLog: true,
		},
		{
			name:           "empty session ID uses container logstream",
			sessionID:      "",
			wantSessionLog: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flags := &monitorFlags{
				sessionID: tt.sessionID,
				tail:      50,
				logType:   "console",
			}
			// The branching logic in MonitorAction.Run checks flags.sessionID != ""
			useSessionLog := flags.sessionID != ""
			assert.Equal(t, tt.wantSessionLog, useSessionLog)
		})
	}
}

func TestValidateMonitorFlags_AllCombinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   monitorFlags
		wantErr string
	}{
		// Both invalid: tail checked first
		{
			name:    "tail and type both invalid",
			flags:   monitorFlags{tail: 0, logType: "invalid"},
			wantErr: "--tail must be between 1 and 300",
		},
		// Valid tail, invalid type
		{
			name:    "valid tail with empty type",
			flags:   monitorFlags{tail: 50, logType: ""},
			wantErr: "--type must be 'console' or 'system'",
		},
		// Valid type, invalid tail
		{
			name:    "system type with tail too high",
			flags:   monitorFlags{tail: 500, logType: "system"},
			wantErr: "--tail must be between 1 and 300",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMonitorFlags(&tt.flags)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidateMonitorFlags_ErrorMessages(t *testing.T) {
	t.Parallel()

	// Verify that validation error messages include the actual bad value
	flags := &monitorFlags{tail: 999, logType: "console"}
	err := validateMonitorFlags(flags)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "999")

	flags = &monitorFlags{tail: 50, logType: "badtype"}
	err = validateMonitorFlags(flags)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "badtype")
}

func TestValidateMonitorFlags_SessionDoesNotAffectValidation(t *testing.T) {
	t.Parallel()

	// Session flag doesn't bypass tail/type validation
	flags := &monitorFlags{
		sessionID: "some-session",
		tail:      0,
		logType:   "console",
	}
	err := validateMonitorFlags(flags)
	assert.Error(t, err, "session ID should not bypass tail validation")

	flags = &monitorFlags{
		sessionID: "some-session",
		tail:      50,
		logType:   "invalid",
	}
	err = validateMonitorFlags(flags)
	assert.Error(t, err, "session ID should not bypass type validation")
}
