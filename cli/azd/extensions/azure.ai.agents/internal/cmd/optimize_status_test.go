// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeStatusCommand_AcceptsOptionalPositionalArg(t *testing.T) {
	cmd := newOptimizeStatusCommand(&azdext.ExtensionContext{})

	// Zero args is now OK (uses last job ID)
	err := cmd.Args(cmd, []string{})
	assert.NoError(t, err)

	// One arg is OK
	err = cmd.Args(cmd, []string{"opt_abc123"})
	assert.NoError(t, err)

	// Two args is rejected
	err = cmd.Args(cmd, []string{"opt_abc123", "extra"})
	assert.Error(t, err)
}

func TestOptimizeStatusCommand_HasWatchFlag(t *testing.T) {
	cmd := newOptimizeStatusCommand(&azdext.ExtensionContext{})

	f := cmd.Flags().Lookup("watch")
	require.NotNil(t, f, "--watch flag should be registered")

	watchVal, err := cmd.Flags().GetBool("watch")
	require.NoError(t, err)
	assert.False(t, watchVal, "--watch should default to false for status")
}

func TestOptimizeStatusCommand_DefaultPollInterval(t *testing.T) {
	cmd := newOptimizeStatusCommand(&azdext.ExtensionContext{})

	pollVal, err := cmd.Flags().GetInt("poll-interval")
	require.NoError(t, err)
	assert.Equal(t, 10, pollVal, "--poll-interval should default to 10")
}

func TestPrintOptimizeJobSummary_ShowsUpdatedAndDuration(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		ID:        "opt-123",
		Status:    optimize_api.StatusCompleted,
		CreatedAt: 1720000000,
		UpdatedAt: 1720000300, // 300 seconds later
	}

	var buf strings.Builder
	printOptimizeJobSummary(&buf, status)
	out := buf.String()

	assert.Contains(t, out, "Created:")
	assert.Contains(t, out, "Updated:")
	assert.Contains(t, out, "Duration:")
	assert.Contains(t, out, "5m0s")
}

func TestPrintOptimizeJobSummary_NoDurationWhenUpdateMissing(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		ID:        "opt-456",
		Status:    optimize_api.StatusRunning,
		CreatedAt: 1720000000,
		UpdatedAt: 0, // not set
	}

	var buf strings.Builder
	printOptimizeJobSummary(&buf, status)
	out := buf.String()

	assert.Contains(t, out, "Created:")
	assert.NotContains(t, out, "Updated:")
	assert.NotContains(t, out, "Duration:")
}

func TestPrintOptimizeJobSummary_NoDurationWhenEqual(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		ID:        "opt-789",
		Status:    optimize_api.StatusCompleted,
		CreatedAt: 1720000000,
		UpdatedAt: 1720000000, // same as created
	}

	var buf strings.Builder
	printOptimizeJobSummary(&buf, status)
	out := buf.String()

	assert.Contains(t, out, "Created:")
	assert.Contains(t, out, "Updated:")
	assert.NotContains(t, out, "Duration:", "duration should not appear when updated == created")
}
