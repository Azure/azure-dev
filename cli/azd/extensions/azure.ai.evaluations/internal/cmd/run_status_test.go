// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The exit code is the whole contract with a pipeline, and there are three
// answers it has to be able to give: the evaluation ran and passed, it ran and
// regressed, or it could not run. The gate owns the middle one; this owns the
// last.
//
// Reporting a run that errored and then exiting 0 tells the pipeline the
// evaluation passed, which is the one answer that is never true.
func TestRunCompleted(t *testing.T) {
	require.NoError(t, runCompleted(nil),
		"nothing was waited for, so there is nothing to report")
	require.NoError(t, runCompleted(&eval_api.OpenAIEvalRun{ID: "r1", Status: "completed"}))
	require.NoError(t, runCompleted(&eval_api.OpenAIEvalRun{ID: "r1", Status: "Completed"}),
		"the service is not consistent about case")
	require.NoError(t, runCompleted(&eval_api.OpenAIEvalRun{ID: "r1"}),
		"a status the service did not send is not a failure to report")

	for _, status := range []string{"failed", "error", "canceled", "cancelled"} {
		err := runCompleted(&eval_api.OpenAIEvalRun{ID: "run_abc", Status: status})
		require.Error(t, err, "status %q must not exit 0", status)
		assert.Contains(t, err.Error(), "run_abc")
		assert.Contains(t, err.Error(), status,
			"the message has to name the status, which is what the caller acts on")
	}
}
