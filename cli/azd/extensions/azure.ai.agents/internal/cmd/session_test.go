// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Command structure tests
// ---------------------------------------------------------------------------

func TestSessionCommand_HasSubcommands(t *testing.T) {
	cmd := newSessionCommand()

	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}

	assert.Contains(t, names, "create")
	assert.Contains(t, names, "show")
	assert.Contains(t, names, "delete")
	assert.Contains(t, names, "list")
}

func TestSessionShowCommand_RequiresOneArg(t *testing.T) {
	cmd := newSessionShowCommand()

	assert.NoError(t, cmd.Args(cmd, []string{"my-session"}))
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.Error(t, cmd.Args(cmd, []string{"a", "b"}))
}

func TestSessionDeleteCommand_RequiresOneArg(t *testing.T) {
	cmd := newSessionDeleteCommand()

	assert.NoError(t, cmd.Args(cmd, []string{"my-session"}))
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.Error(t, cmd.Args(cmd, []string{"a", "b"}))
}

func TestSessionCreateCommand_DefaultFlags(t *testing.T) {
	cmd := newSessionCreateCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "json", output)

	sessionID, _ := cmd.Flags().GetString("session-id")
	assert.Equal(t, "", sessionID)

	version, _ := cmd.Flags().GetString("version")
	assert.Equal(t, "", version)

	isolationKey, _ := cmd.Flags().GetString("isolation-key")
	assert.Equal(t, "", isolationKey)
}

func TestSessionCreateCommand_AcceptsPositionalArgs(t *testing.T) {
	cmd := newSessionCreateCommand()

	assert.NoError(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"my-agent"}))
	assert.NoError(t, cmd.Args(cmd, []string{"my-agent", "3"}))
	assert.NoError(t, cmd.Args(cmd, []string{"my-agent", "3", "sk-key"}))
	assert.Error(t, cmd.Args(cmd, []string{"a", "b", "c", "d"}))
}

func TestSessionListCommand_DefaultFlags(t *testing.T) {
	cmd := newSessionListCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "json", output)

	limit, _ := cmd.Flags().GetInt32("limit")
	assert.Equal(t, int32(0), limit)

	token, _ := cmd.Flags().GetString("pagination-token")
	assert.Equal(t, "", token)
}

// ---------------------------------------------------------------------------
// Output formatting tests
// ---------------------------------------------------------------------------

func TestPrintSessionJSON(t *testing.T) {
	session := &agent_api.AgentSessionResource{
		AgentSessionID: "test-session-1",
		VersionIndicator: agent_api.VersionIndicator{
			Type:         "version_ref",
			AgentVersion: "3",
		},
		Status:         agent_api.AgentSessionStatusActive,
		CreatedAt:      1710234567,
		LastAccessedAt: 1710234567,
		ExpiresAt:      1712826567,
	}

	err := printSessionJSON(session)
	require.NoError(t, err)
}

func TestPrintSessionJSON_Format(t *testing.T) {
	session := &agent_api.AgentSessionResource{
		AgentSessionID: "test-session-1",
		VersionIndicator: agent_api.VersionIndicator{
			Type:         "version_ref",
			AgentVersion: "3",
		},
		Status:         agent_api.AgentSessionStatusActive,
		CreatedAt:      1710234567,
		LastAccessedAt: 1710234567,
		ExpiresAt:      1712826567,
	}

	data, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)

	var result map[string]any
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)

	assert.Equal(t, "test-session-1", result["agent_session_id"])
	assert.Equal(t, "active", result["status"])

	vi := result["version_indicator"].(map[string]any)
	assert.Equal(t, "version_ref", vi["type"])
	assert.Equal(t, "3", vi["agent_version"])

	assert.Equal(t, float64(1710234567), result["created_at"])
}

func TestPrintSessionTable(t *testing.T) {
	session := &agent_api.AgentSessionResource{
		AgentSessionID: "test-session-1",
		VersionIndicator: agent_api.VersionIndicator{
			Type:         "version_ref",
			AgentVersion: "3",
		},
		Status:         agent_api.AgentSessionStatusActive,
		CreatedAt:      1710234567,
		LastAccessedAt: 1710234567,
		ExpiresAt:      1712826567,
	}

	err := printSessionTable(session)
	require.NoError(t, err)
}

func TestPrintSessionListJSON(t *testing.T) {
	nextToken := "abc123"
	result := &agent_api.SessionListResult{
		Data: []agent_api.AgentSessionResource{
			{
				AgentSessionID: "session-1",
				VersionIndicator: agent_api.VersionIndicator{
					Type:         "version_ref",
					AgentVersion: "3",
				},
				Status:         agent_api.AgentSessionStatusActive,
				CreatedAt:      1710234567,
				LastAccessedAt: 1710234567,
				ExpiresAt:      1712826567,
			},
			{
				AgentSessionID: "session-2",
				VersionIndicator: agent_api.VersionIndicator{
					Type:         "version_ref",
					AgentVersion: "1",
				},
				Status:         agent_api.AgentSessionStatusIdle,
				CreatedAt:      1710230000,
				LastAccessedAt: 1710231000,
				ExpiresAt:      1712822000,
			},
		},
		PaginationToken: &nextToken,
	}

	err := printSessionListJSON(result)
	require.NoError(t, err)
}

func TestPrintSessionListTable(t *testing.T) {
	result := &agent_api.SessionListResult{
		Data: []agent_api.AgentSessionResource{
			{
				AgentSessionID: "session-1",
				VersionIndicator: agent_api.VersionIndicator{
					Type:         "version_ref",
					AgentVersion: "3",
				},
				Status:    agent_api.AgentSessionStatusActive,
				CreatedAt: 1710234567,
			},
		},
	}

	err := printSessionListTable(result)
	require.NoError(t, err)
}

func TestPrintSessionListTable_Empty(t *testing.T) {
	result := &agent_api.SessionListResult{
		Data: []agent_api.AgentSessionResource{},
	}

	err := printSessionListTable(result)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Timestamp formatting tests
// ---------------------------------------------------------------------------

func TestFormatUnixTimestamp(t *testing.T) {
	tests := []struct {
		name     string
		epoch    int64
		expected string
	}{
		{
			name:     "zero returns dash",
			epoch:    0,
			expected: "-",
		},
		{
			name:     "known timestamp",
			epoch:    1710234567,
			expected: "2024-03-12T09:09:27Z",
		},
		{
			name:     "unix epoch start",
			epoch:    1,
			expected: "1970-01-01T00:00:01Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatUnixTimestamp(tt.epoch)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---------------------------------------------------------------------------
// Model serialization tests
// ---------------------------------------------------------------------------

func TestAgentSessionResourceJSON_RoundTrip(t *testing.T) {
	session := agent_api.AgentSessionResource{
		AgentSessionID: "my-session",
		VersionIndicator: agent_api.VersionIndicator{
			Type:         "version_ref",
			AgentVersion: "3",
		},
		Status:         agent_api.AgentSessionStatusActive,
		CreatedAt:      1710234567,
		LastAccessedAt: 1710234567,
		ExpiresAt:      1712826567,
	}

	data, err := json.Marshal(session)
	require.NoError(t, err)

	var decoded agent_api.AgentSessionResource
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, session, decoded)
}

func TestCreateAgentSessionRequestJSON(t *testing.T) {
	sessionID := "my-session"
	request := agent_api.CreateAgentSessionRequest{
		AgentSessionID: &sessionID,
		VersionIndicator: &agent_api.VersionIndicator{
			Type:         "version_ref",
			AgentVersion: "3",
		},
	}

	data, err := json.Marshal(request)
	require.NoError(t, err)

	var result map[string]any
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)

	assert.Equal(t, "my-session", result["agent_session_id"])
	vi := result["version_indicator"].(map[string]any)
	assert.Equal(t, "version_ref", vi["type"])
	assert.Equal(t, "3", vi["agent_version"])
}

func TestCreateAgentSessionRequestJSON_NoSessionID(t *testing.T) {
	request := agent_api.CreateAgentSessionRequest{
		VersionIndicator: &agent_api.VersionIndicator{
			Type:         "version_ref",
			AgentVersion: "5",
		},
	}

	data, err := json.Marshal(request)
	require.NoError(t, err)

	var result map[string]any
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)

	_, hasID := result["agent_session_id"]
	assert.False(t, hasID, "agent_session_id should be omitted when nil")
}

func TestCreateAgentSessionRequestJSON_NoVersion(t *testing.T) {
	request := agent_api.CreateAgentSessionRequest{}

	data, err := json.Marshal(request)
	require.NoError(t, err)

	var result map[string]any
	err = json.Unmarshal(data, &result)
	require.NoError(t, err)

	_, hasVI := result["version_indicator"]
	assert.False(t, hasVI, "version_indicator should be omitted when nil")

	_, hasID := result["agent_session_id"]
	assert.False(t, hasID, "agent_session_id should be omitted when nil")
}

func TestSessionListResultJSON_WithPaginationToken(t *testing.T) {
	token := "next-page"
	result := agent_api.SessionListResult{
		Data: []agent_api.AgentSessionResource{
			{
				AgentSessionID: "s1",
				Status:         agent_api.AgentSessionStatusActive,
			},
		},
		PaginationToken: &token,
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded map[string]any
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "next-page", decoded["pagination_token"])
	items := decoded["data"].([]any)
	assert.Len(t, items, 1)
}

func TestSessionListResultJSON_NoPaginationToken(t *testing.T) {
	result := agent_api.SessionListResult{
		Data: []agent_api.AgentSessionResource{},
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded map[string]any
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	_, hasToken := decoded["pagination_token"]
	assert.False(t, hasToken, "pagination_token should be omitted when nil")
}
