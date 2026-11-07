// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
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
		Status:    optimize_api.StatusSucceeded,
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
		Status:    optimize_api.StatusInProgress,
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
		Status:    optimize_api.StatusSucceeded,
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

func TestOptimizeStatusCommand_OutputFlagOptions(t *testing.T) {
	cmd := newOptimizeStatusCommand(&azdext.ExtensionContext{})
	assertOutputFlagOptions(t, cmd, "table", []string{"json", "table"})
}

func TestPrintOptimizeStatusJSON(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		ID:        "opt-json-test",
		Status:    optimize_api.StatusSucceeded,
		CreatedAt: 1720000000,
		UpdatedAt: 1720000300,
	}

	var buf strings.Builder
	err := printOptimizeStatusJSON(&buf, status)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, `"id": "opt-json-test"`)
	assert.Contains(t, out, `"status": "succeeded"`)
	assert.Contains(t, out, `"created_at": 1720000000`)
}

func TestPollOptimizeJobJSONIsSilentAndStopsOnSkipped(t *testing.T) {
	t.Parallel()
	body := `{"id":"job1","status":"skipped","result":{"termination_reason":"no_candidates",
		"token_usage":[],"latency_metrics":[]},"created_at":1,"updated_at":2,"run_duration_ms":0}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/agent_optimization_jobs/job1", r.URL.Path, "JSON polling must not fetch display-only candidates")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	job, err := pollOptimizeJob(cmd, newTestOptimizeClient(server.URL), 1, "job1", "", true)
	require.NoError(t, err)
	assert.Empty(t, output.String(), "no progress tables or spinners may pollute JSON stdout")
	require.NoError(t, printOptimizeStatusJSON(&output, job))
	assert.JSONEq(t, body, output.String())
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
}

func TestOptimizeJobError(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, optimizeJobError(&optimize_api.OptimizeJobStatus{
		ID: "job1", Status: optimize_api.StatusFailed,
	}), "failed")
	require.ErrorContains(t, optimizeJobError(&optimize_api.OptimizeJobStatus{
		Error: &optimize_api.JobError{Message: "service failure"},
	}), "service failure")
	require.NoError(t, optimizeJobError(&optimize_api.OptimizeJobStatus{Status: optimize_api.StatusSucceeded}))
}
