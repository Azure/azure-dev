// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pipeline that starts a run with --no-wait has to come back for it. What it
// needs to do that is a fixed shape this extension controls, not whatever the
// API happened to return.
func TestStartedRunIsTheHandoffAPipelineNeeds(t *testing.T) {
	run := &eval_api.OpenAIEvalRun{
		ID:        "evalrun_01JQZX",
		EvalID:    "eval_ignored",
		Status:    "queued",
		CreatedAt: "2026-07-31T21:04:11Z",
		Metadata:  map[string]string{"azd_eval": "support-agent-smoke"},
		DataSource: &eval_api.EvalRunDataSource{
			Type: eval_api.EvalRunDataSourceTypeTraces,
		},
	}

	raw, err := json.Marshal(startedRun(run, "eval_01JQZW", &project.Eval{Name: "support-agent-smoke"}))
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))

	assert.Equal(t, "evalrun_01JQZX", out["run_id"])
	assert.Equal(t, "support-agent-smoke", out["eval_name"])
	assert.Equal(t, "queued", out["status"])
	assert.Equal(t, "2026-07-31T21:04:11Z", out["created_at"])

	// The eval the run was started against, which is the one the command
	// resolved rather than whatever the run echoed back.
	assert.Equal(t, "eval_01JQZW", out["eval_id"])

	// Nothing the extension does not promise. A pipeline that could read the
	// data source here would come to depend on it.
	for _, leaked := range []string{"data_source", "metadata", "id", "report_url"} {
		assert.NotContains(t, out, leaked,
			"the handoff must not leak %q from the service object", leaked)
	}
}

// A script logging created_at should not have to know which route produced
// the run: the service sends epoch seconds here and a formatted string
// elsewhere, so the handoff settles on one.
func TestStartedRunNormalizesTheTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"epoch seconds", float64(1785801525), "2026-08-03T23:58:45Z"},
		{"already formatted", "2026-07-31T21:04:11Z", "2026-07-31T21:04:11Z"},
		{"absent", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handoff := startedRun(
				&eval_api.OpenAIEvalRun{ID: "evalrun_1", CreatedAt: tc.value}, "eval_1", nil)
			assert.Equal(t, tc.want, handoff.CreatedAt)
		})
	}
}

// An empty one is omitted rather than reported as "", which a script would
// otherwise print as the eval's name.
func TestStartedRunOmitsTheNameItDoesNotHave(t *testing.T) {
	raw, err := json.Marshal(startedRun(
		&eval_api.OpenAIEvalRun{ID: "evalrun_1", Status: "queued"}, "eval_1", nil))
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.NotContains(t, out, "eval_name")
	assert.Equal(t, "eval_1", out["eval_id"])
}
