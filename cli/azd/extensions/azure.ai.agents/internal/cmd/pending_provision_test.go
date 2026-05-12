// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestParsePendingProvisionReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"single", "project", []string{"project"}},
		{"single trimmed", "  project  ", []string{"project"}},
		{"multiple sorted", "project,model_deployment", []string{"model_deployment", "project"}},
		{"duplicates", "project,project,model_deployment", []string{"model_deployment", "project"}},
		{"with empty segments", "project,,model_deployment,", []string{"model_deployment", "project"}},
		{"all empty segments", ",,,", nil},
		{"with whitespace segments", "  ,project ,  ,model_deployment  ", []string{"model_deployment", "project"}},
		{"unknown tag preserved", "future_tag,project", []string{"future_tag", "project"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, parsePendingProvisionReasons(tc.in))
		})
	}
}

func TestFormatPendingProvisionReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"nil", nil, ""},
		{"empty", []string{}, ""},
		{"single", []string{"project"}, "project"},
		{"sorts and dedups", []string{"project", "acr", "project"}, "acr,project"},
		{"trims whitespace", []string{"  project  ", "acr"}, "acr,project"},
		{"all empty", []string{"", " "}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, formatPendingProvisionReasons(tc.in))
		})
	}
}

func TestAddPendingProvisionReason(t *testing.T) {
	t.Parallel()

	t.Run("adds to empty env var", func(t *testing.T) {
		t.Parallel()

		envServer := &testEnvironmentServiceServer{
			environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
		}
		azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

		out, err := addPendingProvisionReason(context.Background(), azdClient, "test-env", pendingReasonModelDeployment)
		require.NoError(t, err)
		require.Equal(t, []string{pendingReasonModelDeployment}, out)
		require.Equal(t, pendingReasonModelDeployment, envServer.values["test-env"][pendingProvisionEnvVar])
	})

	t.Run("appends to existing list", func(t *testing.T) {
		t.Parallel()

		envServer := &testEnvironmentServiceServer{
			environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
			values: map[string]map[string]string{
				"test-env": {pendingProvisionEnvVar: "project"},
			},
		}
		azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

		out, err := addPendingProvisionReason(context.Background(), azdClient, "test-env", pendingReasonACR)
		require.NoError(t, err)
		require.Equal(t, []string{pendingReasonACR, "project"}, out)
		require.Equal(t, "acr,project", envServer.values["test-env"][pendingProvisionEnvVar])
	})

	t.Run("duplicate is no-op", func(t *testing.T) {
		t.Parallel()

		envServer := &testEnvironmentServiceServer{
			environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
			values: map[string]map[string]string{
				"test-env": {pendingProvisionEnvVar: "model_deployment,project"},
			},
		}
		azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

		out, err := addPendingProvisionReason(context.Background(), azdClient, "test-env", pendingReasonProject)
		require.NoError(t, err)
		require.Equal(t, []string{pendingReasonModelDeployment, pendingReasonProject}, out)
		// Value unchanged from initial state (round-trips through parse/format).
		require.Equal(t, "model_deployment,project", envServer.values["test-env"][pendingProvisionEnvVar])
	})

	t.Run("normalizes prior malformed value before adding", func(t *testing.T) {
		t.Parallel()

		envServer := &testEnvironmentServiceServer{
			environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
			values: map[string]map[string]string{
				"test-env": {pendingProvisionEnvVar: "  project,,project ,"},
			},
		}
		azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

		out, err := addPendingProvisionReason(context.Background(), azdClient, "test-env", pendingReasonModelDeployment)
		require.NoError(t, err)
		require.Equal(t, []string{pendingReasonModelDeployment, pendingReasonProject}, out)
		require.Equal(t, "model_deployment,project", envServer.values["test-env"][pendingProvisionEnvVar])
	})
}

func TestRemovePendingProvisionReason(t *testing.T) {
	t.Parallel()

	t.Run("removes existing tag", func(t *testing.T) {
		t.Parallel()

		envServer := &testEnvironmentServiceServer{
			environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
			values: map[string]map[string]string{
				"test-env": {pendingProvisionEnvVar: "acr,model_deployment,project"},
			},
		}
		azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

		out, err := removePendingProvisionReason(context.Background(), azdClient, "test-env", pendingReasonModelDeployment)
		require.NoError(t, err)
		require.Equal(t, []string{pendingReasonACR, pendingReasonProject}, out)
		require.Equal(t, "acr,project", envServer.values["test-env"][pendingProvisionEnvVar])
	})

	t.Run("removing non-existent tag is no-op", func(t *testing.T) {
		t.Parallel()

		envServer := &testEnvironmentServiceServer{
			environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
			values: map[string]map[string]string{
				"test-env": {pendingProvisionEnvVar: "project"},
			},
		}
		azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

		out, err := removePendingProvisionReason(context.Background(), azdClient, "test-env", pendingReasonACR)
		require.NoError(t, err)
		require.Equal(t, []string{pendingReasonProject}, out)
		require.Equal(t, "project", envServer.values["test-env"][pendingProvisionEnvVar])
	})

	t.Run("removing from unset env var is no-op", func(t *testing.T) {
		t.Parallel()

		envServer := &testEnvironmentServiceServer{
			environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
		}
		azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

		out, err := removePendingProvisionReason(context.Background(), azdClient, "test-env", pendingReasonProject)
		require.NoError(t, err)
		require.Empty(t, out)
		// No write should have happened — env var stays unset.
		_, hit := envServer.values["test-env"][pendingProvisionEnvVar]
		require.False(t, hit)
	})

	t.Run("removing last tag writes empty string", func(t *testing.T) {
		t.Parallel()

		envServer := &testEnvironmentServiceServer{
			environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
			values: map[string]map[string]string{
				"test-env": {pendingProvisionEnvVar: "project"},
			},
		}
		azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

		out, err := removePendingProvisionReason(context.Background(), azdClient, "test-env", pendingReasonProject)
		require.NoError(t, err)
		require.Empty(t, out)
		require.Equal(t, "", envServer.values["test-env"][pendingProvisionEnvVar])
	})
}

func TestClearPendingProvisionReasons(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
		values: map[string]map[string]string{
			"test-env": {pendingProvisionEnvVar: "acr,model_deployment,project"},
		},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

	err := clearPendingProvisionReasons(context.Background(), azdClient, "test-env")
	require.NoError(t, err)
	require.Equal(t, "", envServer.values["test-env"][pendingProvisionEnvVar])
}

func TestPendingProvisionRoundTrip(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{"test-env": {Name: "test-env"}},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
	ctx := context.Background()

	// Sequence: add project → add model_deployment → add acr → remove
	// project → clear. Verifies parse/format consistency, idempotence on
	// duplicates, and end-state cleanliness.
	_, err := addPendingProvisionReason(ctx, azdClient, "test-env", pendingReasonProject)
	require.NoError(t, err)
	_, err = addPendingProvisionReason(ctx, azdClient, "test-env", pendingReasonModelDeployment)
	require.NoError(t, err)
	_, err = addPendingProvisionReason(ctx, azdClient, "test-env", pendingReasonACR)
	require.NoError(t, err)
	require.Equal(t, "acr,model_deployment,project", envServer.values["test-env"][pendingProvisionEnvVar])

	// Re-add a duplicate — value should be unchanged.
	_, err = addPendingProvisionReason(ctx, azdClient, "test-env", pendingReasonACR)
	require.NoError(t, err)
	require.Equal(t, "acr,model_deployment,project", envServer.values["test-env"][pendingProvisionEnvVar])

	_, err = removePendingProvisionReason(ctx, azdClient, "test-env", pendingReasonProject)
	require.NoError(t, err)
	require.Equal(t, "acr,model_deployment", envServer.values["test-env"][pendingProvisionEnvVar])

	err = clearPendingProvisionReasons(ctx, azdClient, "test-env")
	require.NoError(t, err)
	require.Equal(t, "", envServer.values["test-env"][pendingProvisionEnvVar])
}
