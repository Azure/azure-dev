// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/recordproxy"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newVersionedSessionTestProject(t *testing.T) (string, *testEnvironmentServiceServer) {
	t.Helper()
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", endpoint)
	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*azdext.ServiceConfig{
			"target": {Name: "target", Host: AiAgentHost, AdditionalProperties: mustStruct(t, map[string]any{
				"kind": "hosted", "name": "agent",
			})},
		},
	}}
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values: map[string]map[string]string{"test": {
			"AGENT_TARGET_NAME": "agent", "AGENT_TARGET_PROJECT_ENDPOINT": endpoint, "AGENT_TARGET_VERSION": "2",
			"AGENT_TARGET_ENDPOINT":                   endpoint + "/agents/agent/versions/2",
			"AGENT_TARGET_RESPONSES_ENDPOINT":         endpoint + "/agents/agent/endpoint/protocols/openai/responses",
			"AGENT_TARGET_PROTOCOL_ENDPOINTS_VERSION": "1",
		}},
	}
	t.Setenv("AZD_SERVER", newInvokeRemoteContextTestAzdServer(t, projectServer, envServer))
	return endpoint, envServer
}

func TestSessionCreateThenInvokeUsesRequestedVersion(t *testing.T) {
	endpoint, _ := newVersionedSessionTestProject(t)
	oldKey := buildAgentKey(endpoint, "agent", "2", false)
	key := buildAgentKey(endpoint, "agent", "7", false)
	saveAgentStateForTest(t, nil, oldKey, agentStateRecord{SessionID: "cached-v2"})
	previous := recordproxy.Transport
	t.Cleanup(func() { recordproxy.Transport = previous })
	var creates int
	recordproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		creates++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/projects/project/agents/agent/endpoint/sessions", r.URL.Path)
		var request agent_api.CreateAgentSessionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.NotNil(t, request.VersionIndicator)
		require.Equal(t, "7", request.VersionIndicator.AgentVersion)
		return &http.Response{
			StatusCode: http.StatusOK, Request: r, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"agent_session_id":"created-v7","version_indicator":{"type":"version_ref","agent_version":"7"}}`,
			)),
		}, nil
	})
	action := &SessionCreateAction{
		flags:      &sessionCreateFlags{sessionFlags: sessionFlags{agentName: "target", noPrompt: true}, version: "7"},
		credential: responseTestCredential{},
	}
	_, err := captureStdout(t, func() error { return action.Run(t.Context()) })
	require.NoError(t, err)
	require.Equal(t, 1, creates)
	previousCreate := createInvokeVersionSession
	t.Cleanup(func() { createInvokeVersionSession = previousCreate })
	var unexpectedCreates int
	createInvokeVersionSession = func(context.Context, string, string, string, string) (*agent_api.AgentSessionResource, error) {
		unexpectedCreates++
		return &agent_api.AgentSessionResource{AgentSessionID: "unnecessary-v7"}, nil
	}
	invoke := &InvokeAction{flags: &invokeFlags{name: "target", version: "7", protocol: "invocations"}, noPrompt: true}
	rc, err := invoke.resolveRemoteContextForInvoke(t.Context())
	require.NoError(t, err)
	defer rc.azdClient.Close()
	id, err := invoke.resolveRemoteSessionID(t.Context(), rc)
	require.NoError(t, err)
	assert.Equal(t, "created-v7", id)
	assert.Zero(t, unexpectedCreates)
	assert.Equal(t, "cached-v2", loadAgentStateForTest(t, rc.azdClient, oldKey).SessionID)
	assert.Equal(t, "created-v7", loadAgentStateForTest(t, rc.azdClient, key).SessionID)
}

func TestSessionDeleteClearsRequestedVersionBeforeInvoke(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			endpoint, _ := newVersionedSessionTestProject(t)
			oldKey := buildAgentKey(endpoint, "agent", "2", false)
			key := buildAgentKey(endpoint, "agent", "7", false)
			otherKey := buildAgentKey(endpoint, "other-agent", "7", false)
			otherProjectKey := buildAgentKey(strings.Replace(endpoint, "/project", "/other-project", 1), "agent", "7", false)
			saveAgentStateForTest(t, nil, oldKey, agentStateRecord{SessionID: "cached-v2"})
			saveAgentStateForTest(t, nil, key, agentStateRecord{
				SessionID: "cached-v7", ConversationID: "conv-v7", Response: &savedResponse{ResponseID: "resp-v7"},
			})
			for _, other := range []string{otherKey, otherProjectKey} {
				saveAgentStateForTest(t, nil, other, agentStateRecord{SessionID: "cached-v7"})
			}
			previous := recordproxy.Transport
			t.Cleanup(func() { recordproxy.Transport = previous })
			var deletes int
			recordproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				deletes++
				require.Equal(t, http.MethodDelete, r.Method)
				require.Equal(t, "/api/projects/project/agents/agent/endpoint/sessions/cached-v7", r.URL.Path)
				return &http.Response{
					StatusCode: status, Request: r,
					Header: http.Header{"Content-Type": {"application/json"}},
					Body:   io.NopCloser(strings.NewReader(`{"error":{"code":"session_not_found","message":"test"}}`)),
				}, nil
			})
			action := &SessionDeleteAction{
				flags:     &sessionDeleteFlags{sessionFlags: sessionFlags{agentName: "target", noPrompt: true}},
				sessionID: "cached-v7", credential: responseTestCredential{},
			}
			_, err := captureStdout(t, func() error { return action.Run(t.Context()) })
			if status == http.StatusNoContent {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, 1, deletes)
			previousCreate := createInvokeVersionSession
			t.Cleanup(func() { createInvokeVersionSession = previousCreate })
			var creates int
			createInvokeVersionSession = func(
				context.Context, string, string, string, string,
			) (*agent_api.AgentSessionResource, error) {
				creates++
				return &agent_api.AgentSessionResource{AgentSessionID: "replacement-v7"}, nil
			}
			invoke := &InvokeAction{flags: &invokeFlags{name: "target", version: "7", protocol: "invocations"}, noPrompt: true}
			rc, err := invoke.resolveRemoteContextForInvoke(t.Context())
			require.NoError(t, err)
			defer rc.azdClient.Close()
			id, err := invoke.resolveRemoteSessionID(t.Context(), rc)
			require.NoError(t, err)
			if status == http.StatusForbidden {
				assert.Equal(t, "cached-v7", id)
				assert.Zero(t, creates)
			} else {
				assert.Equal(t, "replacement-v7", id)
				assert.Equal(t, 1, creates)
			}
			state := loadAgentStateForTest(t, rc.azdClient, key)
			require.NotNil(t, state)
			assert.Equal(t, "conv-v7", state.ConversationID)
			require.NotNil(t, state.Response)
			assert.Equal(t, "resp-v7", state.Response.ResponseID)
			assert.Equal(t, "cached-v2", loadAgentStateForTest(t, rc.azdClient, oldKey).SessionID)
			for _, other := range []string{otherKey, otherProjectKey} {
				assert.Equal(t, "cached-v7", loadAgentStateForTest(t, rc.azdClient, other).SessionID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Command structure tests
// ---------------------------------------------------------------------------

func TestSessionCommand_HasSubcommands(t *testing.T) {
	cmd := newSessionCommand(nil)

	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}

	assert.Contains(t, names, "create")
	assert.Contains(t, names, "show")
	assert.Contains(t, names, "stop")
	assert.Contains(t, names, "delete")
	assert.Contains(t, names, "list")
}

func TestSessionStopCommand_RequiresOneArg(t *testing.T) {
	cmd := newSessionStopCommand(nil)

	assert.NoError(t, cmd.Args(cmd, []string{"my-session"}))
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.Error(t, cmd.Args(cmd, []string{"a", "b"}))
}

func TestSessionShowCommand_RequiresOneArg(t *testing.T) {
	cmd := newSessionShowCommand(nil)

	assert.NoError(t, cmd.Args(cmd, []string{"my-session"}))
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.Error(t, cmd.Args(cmd, []string{"a", "b"}))
}

func TestSessionDeleteCommand_RequiresOneArg(t *testing.T) {
	cmd := newSessionDeleteCommand(nil)

	assert.NoError(t, cmd.Args(cmd, []string{"my-session"}))
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.Error(t, cmd.Args(cmd, []string{"a", "b"}))
}

func TestSessionCreateCommand_DefaultFlags(t *testing.T) {
	cmd := newSessionCreateCommand(nil)

	assertOutputFlagOptions(t, cmd, "json", []string{"json", "table"})

	sessionID, _ := cmd.Flags().GetString("session-id")
	assert.Equal(t, "", sessionID)

	version, _ := cmd.Flags().GetString("version")
	assert.Equal(t, "", version)

	userIdentity, _ := cmd.Flags().GetString("user-identity")
	assert.Equal(t, "", userIdentity)
}

func TestSessionCreateCommand_AcceptsPositionalArgs(t *testing.T) {
	cmd := newSessionCreateCommand(nil)

	assert.NoError(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"my-agent"}))
	assert.NoError(t, cmd.Args(cmd, []string{"my-agent", "3"}))
	assert.Error(t, cmd.Args(cmd, []string{"a", "b", "c"}))
}

func TestSessionListCommand_DefaultFlags(t *testing.T) {
	cmd := newSessionListCommand(nil)

	assertOutputFlagOptions(t, cmd, "json", []string{"json", "table"})

	limit, _ := cmd.Flags().GetInt32("limit")
	assert.Equal(t, int32(0), limit)

	token, _ := cmd.Flags().GetString("pagination-token")
	assert.Equal(t, "", token)

	userIdentity, _ := cmd.Flags().GetString("user-identity")
	assert.Equal(t, "", userIdentity)
}

func TestSessionShowCommand_HasUserIdentityFlag(t *testing.T) {
	cmd := newSessionShowCommand(nil)

	for _, name := range []string{"user-identity"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "expected flag %q", name)
		assert.Equal(t, "", f.DefValue)
	}
}

func TestSessionDeleteCommand_HasUserIdentityFlag(t *testing.T) {
	cmd := newSessionDeleteCommand(nil)

	for _, name := range []string{"user-identity"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "expected flag %q", name)
		assert.Equal(t, "", f.DefValue)
	}
}

func TestSessionStopCommand_HasUserIdentityFlag(t *testing.T) {
	cmd := newSessionStopCommand(nil)

	for _, name := range []string{"user-identity"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "expected flag %q", name)
		assert.Equal(t, "", f.DefValue)
	}
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

// ---------------------------------------------------------------------------
// Error classification tests — mirrors logic from session show / delete
// ---------------------------------------------------------------------------

// classifyGetSessionError reproduces the error handling from newSessionShowCommand
// so we can test the classification without an end-to-end context.
func classifyGetSessionError(err error, sessionID string) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
		respErr.StatusCode == http.StatusNotFound {
		return exterrors.Validation(
			exterrors.CodeSessionNotFound,
			fmt.Sprintf(
				"session %q not found or has been deleted",
				sessionID,
			),
			"use 'azd ai agent sessions list' to see "+
				"available sessions",
		)
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpGetSession)
}

func TestGetSession_404_ProducesValidationError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "session_not_found",
	}

	result := classifyGetSessionError(azErr, "my-session-id")
	require.Error(t, result)

	// Should produce a LocalError (validation), not a ServiceError.
	var localErr *azdext.LocalError
	require.True(
		t, errors.As(result, &localErr),
		"404 should produce a LocalError, got: %T", result,
	)
	assert.Equal(t, exterrors.CodeSessionNotFound, localErr.Code)
	assert.Contains(t, localErr.Message, "my-session-id")
	assert.Contains(t, localErr.Message, "not found")
}

func TestGetSession_500_ProducesServiceError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "internal_error",
	}

	result := classifyGetSessionError(azErr, "sess-1")
	require.Error(t, result)

	// Non-404 errors remain as ServiceError.
	var svcErr *azdext.ServiceError
	require.True(
		t, errors.As(result, &svcErr),
		"500 should produce a ServiceError, got: %T", result,
	)
}

// classifyDeleteSessionError reproduces the error handling from newSessionDeleteCommand
// so we can test the classification without an end-to-end context.
func classifyDeleteSessionError(err error, sessionID string) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
		respErr.StatusCode == http.StatusNotFound {
		return exterrors.Validation(
			exterrors.CodeSessionNotFound,
			fmt.Sprintf(
				"session %q not found or has already been deleted",
				sessionID,
			),
			"use 'azd ai agent sessions list' to see "+
				"available sessions",
		)
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpDeleteSession)
}

func TestDeleteSession_404_ProducesValidationError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "session_not_found",
	}

	result := classifyDeleteSessionError(azErr, "my-session-id")
	require.Error(t, result)

	// Should produce a LocalError (validation), not a ServiceError.
	var localErr *azdext.LocalError
	require.True(
		t, errors.As(result, &localErr),
		"404 should produce a LocalError, got: %T", result,
	)
	assert.Equal(t, exterrors.CodeSessionNotFound, localErr.Code)
	assert.Contains(t, localErr.Message, "my-session-id")
	assert.Contains(t, localErr.Message, "not found")
}

func TestDeleteSession_500_ProducesServiceError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "internal_error",
	}

	result := classifyDeleteSessionError(azErr, "sess-1")
	require.Error(t, result)

	// Non-404 errors remain as ServiceError.
	var svcErr *azdext.ServiceError
	require.True(
		t, errors.As(result, &svcErr),
		"500 should produce a ServiceError, got: %T", result,
	)
}

// classifyStopSessionError reproduces the error handling from
// newSessionStopCommand so we can test the classification without an
// end-to-end context. It returns nil for errors that 'stop' treats as
// an idempotent success (an already-stopped session).
func classifyStopSessionError(err error, sessionID string) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		if respErr.StatusCode == http.StatusConflict &&
			respErr.ErrorCode == "session_already_stopped" {
			return nil
		}

		if respErr.StatusCode == http.StatusNotFound {
			return exterrors.Validation(
				exterrors.CodeSessionNotFound,
				fmt.Sprintf(
					"session %q not found or has been deleted",
					sessionID,
				),
				"use 'azd ai agent sessions list' to see "+
					"available sessions",
			)
		}
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpStopSession)
}

func TestStopSession_409AlreadyStopped_IsSuccess(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusConflict,
		ErrorCode:  "session_already_stopped",
	}

	result := classifyStopSessionError(azErr, "my-session-id")
	require.NoError(
		t, result,
		"an already-stopped session should be treated as success",
	)
}

func TestStopSession_409Other_ProducesServiceError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusConflict,
		ErrorCode:  "session_conflict",
	}

	result := classifyStopSessionError(azErr, "my-session-id")
	require.Error(
		t, result,
		"an unrelated 409 should still surface as an error",
	)

	var svcErr *azdext.ServiceError
	require.True(
		t, errors.As(result, &svcErr),
		"unrelated 409 should produce a ServiceError, got: %T", result,
	)
}

func TestStopSession_404_ProducesValidationError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "session_not_found",
	}

	result := classifyStopSessionError(azErr, "my-session-id")
	require.Error(t, result)

	var localErr *azdext.LocalError
	require.True(
		t, errors.As(result, &localErr),
		"404 should produce a LocalError, got: %T", result,
	)
	assert.Equal(t, exterrors.CodeSessionNotFound, localErr.Code)
	assert.Contains(t, localErr.Message, "my-session-id")
	assert.Contains(t, localErr.Message, "not found")
}

func TestStopSession_500_ProducesServiceError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "internal_error",
	}

	result := classifyStopSessionError(azErr, "sess-1")
	require.Error(t, result)

	var svcErr *azdext.ServiceError
	require.True(
		t, errors.As(result, &svcErr),
		"500 should produce a ServiceError, got: %T", result,
	)
}
