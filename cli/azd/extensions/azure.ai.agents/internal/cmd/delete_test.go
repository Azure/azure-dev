// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"net/http"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteMarkerCleanup(t *testing.T) {
	t.Parallel()

	t.Run("whole agent clears readiness and endpoint markers", func(t *testing.T) {
		envServer := &testEnvironmentServiceServer{
			current: &azdext.Environment{Name: "dev"},
			values: map[string]map[string]string{
				"dev": {"AGENT_MY_AGENT_PROJECT_ENDPOINT": "https://account.test/projects/current"},
			},
		}
		client := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
		action := &DeleteAction{}
		action.cleanupEnvVars(t.Context(), client, "my-agent", "https://account.test/projects/current")
		for _, key := range []string{
			"AGENT_MY_AGENT_NAME",
			"AGENT_MY_AGENT_VERSION",
			"AGENT_MY_AGENT_ENDPOINT",
			"AGENT_MY_AGENT_TARGET_NAME",
			"AGENT_MY_AGENT_TARGET_VERSION",
			"AGENT_MY_AGENT_PROJECT_ENDPOINT",
			"AGENT_MY_AGENT_RESPONSES_ENDPOINT",
			"AGENT_MY_AGENT_INVOCATIONS_ENDPOINT",
			"AGENT_MY_AGENT_INVOCATIONS_WS_ENDPOINT",
		} {
			got, ok := envServer.values["dev"][key]
			require.True(t, ok, "%s was not cleared", key)
			require.Equal(t, "", got, key)
		}
	})

	t.Run("whole agent markers are preserved for another project", func(t *testing.T) {
		envServer := &testEnvironmentServiceServer{
			current: &azdext.Environment{Name: "dev"},
			values: map[string]map[string]string{
				"dev": {
					"AGENT_MY_AGENT_VERSION":          "2",
					"AGENT_MY_AGENT_PROJECT_ENDPOINT": "https://account.test/projects/other",
				},
			},
		}
		client := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
		action := &DeleteAction{}
		action.cleanupEnvVars(t.Context(), client, "my-agent", "https://account.test/projects/current")
		require.Equal(t, "2", envServer.values["dev"]["AGENT_MY_AGENT_VERSION"])
	})

	t.Run("version marker clears only when matching", func(t *testing.T) {
		envServer := &testEnvironmentServiceServer{
			current: &azdext.Environment{Name: "dev"},
			values: map[string]map[string]string{
				"dev": {
					"AGENT_MY_AGENT_VERSION":          "2",
					"AGENT_MY_AGENT_PROJECT_ENDPOINT": "https://account.test/api/projects/current",
				},
			},
		}
		client := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
		action := &DeleteAction{}
		action.clearDeletedVersionMarker(
			t.Context(), client, "my-agent", "1", "https://account.test/projects/current",
		)
		require.Equal(t, "2", envServer.values["dev"]["AGENT_MY_AGENT_VERSION"])
		action.clearDeletedVersionMarker(
			t.Context(), client, "my-agent", "2", "https://account.test/projects/current",
		)
		for _, key := range []string{
			"AGENT_MY_AGENT_VERSION",
			"AGENT_MY_AGENT_ENDPOINT",
			"AGENT_MY_AGENT_TARGET_NAME",
			"AGENT_MY_AGENT_TARGET_VERSION",
			"AGENT_MY_AGENT_RESPONSES_ENDPOINT",
			"AGENT_MY_AGENT_INVOCATIONS_ENDPOINT",
			"AGENT_MY_AGENT_INVOCATIONS_WS_ENDPOINT",
		} {
			got, ok := envServer.values["dev"][key]
			require.True(t, ok, "%s was not cleared", key)
			require.Equal(t, "", got, key)
		}
	})

	t.Run("version marker uses legacy agent endpoint scope", func(t *testing.T) {
		envServer := &testEnvironmentServiceServer{
			current: &azdext.Environment{Name: "dev"},
			values: map[string]map[string]string{
				"dev": {
					"AGENT_MY_AGENT_VERSION":  "2",
					"AGENT_MY_AGENT_ENDPOINT": "https://account.test/api/projects/current/agents/my-agent/versions/2",
				},
			},
		}
		client := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
		action := &DeleteAction{}
		action.clearDeletedVersionMarker(
			t.Context(), client, "my-agent", "2", "https://account.test/projects/current",
		)
		got, ok := envServer.values["dev"]["AGENT_MY_AGENT_VERSION"]
		require.True(t, ok)
		require.Empty(t, got)
	})

	t.Run("version marker is preserved for another project", func(t *testing.T) {
		envServer := &testEnvironmentServiceServer{
			current: &azdext.Environment{Name: "dev"},
			values: map[string]map[string]string{
				"dev": {
					"AGENT_MY_AGENT_VERSION":          "2",
					"AGENT_MY_AGENT_PROJECT_ENDPOINT": "https://account.test/projects/other",
				},
			},
		}
		client := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
		action := &DeleteAction{}
		action.clearDeletedVersionMarker(
			t.Context(), client, "my-agent", "2", "https://account.test/projects/current",
		)
		require.Equal(t, "2", envServer.values["dev"]["AGENT_MY_AGENT_VERSION"])
	})
}

func TestDeleteCommand_AcceptsPositionalArg(t *testing.T) {
	cmd := newDeleteCommand(nil)
	err := cmd.Args(cmd, []string{"my-agent"})
	assert.NoError(t, err)
}

func TestDeleteCommand_AcceptsNoArgs(t *testing.T) {
	cmd := newDeleteCommand(nil)
	err := cmd.Args(cmd, []string{})
	assert.NoError(t, err)
}

func TestDeleteCommand_RejectsMultipleArgs(t *testing.T) {
	cmd := newDeleteCommand(nil)
	err := cmd.Args(cmd, []string{"svc1", "svc2"})
	assert.Error(t, err)
}

func TestDeleteCommand_ForceFlag(t *testing.T) {
	cmd := newDeleteCommand(nil)
	flag := cmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected --force flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Fatalf("expected --force default false, got %q", flag.DefValue)
	}
	assert.Contains(t, flag.Usage, "no-prompt")
}

func TestDeleteCommand_OutputFlagAnnotation(t *testing.T) {
	cmd := newDeleteCommand(nil)
	// RegisterFlagOptions stores allowed values in annotations
	require.NotNil(t, cmd.Annotations)
}

func TestDeleteCommand_VersionFlag(t *testing.T) {
	cmd := newDeleteCommand(nil)
	flag := cmd.Flags().Lookup("version")
	if flag == nil {
		t.Fatal("expected --version flag to be registered")
	}
	if flag.DefValue != "" {
		t.Fatalf("expected --version default empty, got %q", flag.DefValue)
	}
}

func TestDeleteConfirmation_NoPromptRequiresForce(t *testing.T) {
	action := &DeleteAction{flags: &deleteFlags{noPrompt: true}}

	err := action.confirmDelete(t.Context(), nil, "my-agent")

	require.Error(t, err)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeDeleteRequiresForce, localErr.Code)
	assert.Contains(t, localErr.Suggestion, "--force")
}

func TestDeleteConfirmation_NoPromptForcePreConsents(t *testing.T) {
	action := &DeleteAction{flags: &deleteFlags{noPrompt: true, force: true}}
	require.NoError(t, action.confirmDelete(t.Context(), nil, "my-agent"))
}

// ---------------------------------------------------------------------------
// Error classification tests — calls the real classifyDeleteError from delete.go
// ---------------------------------------------------------------------------

func TestDeleteAgent_404_ProducesValidationError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "not_found",
	}

	result := classifyDeleteError(azErr, "my-agent")
	require.Error(t, result)

	var localErr *azdext.LocalError
	require.True(
		t, errors.As(result, &localErr),
		"404 should produce a LocalError, got: %T", result,
	)
	assert.Equal(t, exterrors.CodeAgentNotFound, localErr.Code)
	assert.Contains(t, localErr.Message, "my-agent")
	assert.Contains(t, localErr.Message, "not found")
}

func TestDeleteAgent_409_ProducesValidationError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusConflict,
		ErrorCode:  "conflict",
	}

	result := classifyDeleteError(azErr, "my-agent")
	require.Error(t, result)

	var localErr *azdext.LocalError
	require.True(
		t, errors.As(result, &localErr),
		"409 should produce a LocalError, got: %T", result,
	)
	assert.Equal(t, exterrors.CodeAgentHasActiveSessions, localErr.Code)
	assert.Contains(t, localErr.Message, "active sessions")
	assert.Contains(t, localErr.Suggestion, "--force")
}

func TestDeleteAgent_500_ProducesServiceError(t *testing.T) {
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "internal_error",
	}

	result := classifyDeleteError(azErr, "my-agent")
	require.Error(t, result)

	var svcErr *azdext.ServiceError
	require.True(
		t, errors.As(result, &svcErr),
		"500 should produce a ServiceError, got: %T", result,
	)
}
