// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type runSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ResultCounts *struct {
		Passed  int `json:"passed"`
		Failed  int `json:"failed"`
		Errored int `json:"errored"`
	} `json:"result_counts"`
}

func TestCLIRunList(t *testing.T) {
	f := sharedEval(t)

	t.Run("table", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "list", f.EvalID))
		for _, header := range []string{"RUN ID", "NAME", "STATUS", "RESULTS"} {
			require.Containsf(t, r.Stdout, header, "the listing lost its %s column", header)
		}
		require.Contains(t, r.Stdout, f.BaselineRunID)
		require.Contains(t, r.Stdout, f.TreatmentRunID)
		require.Contains(t, r.Stdout, "1 passed, 2 failed, 0 errored",
			"the listing must summarise each run's counts, not just its status")
	})

	t.Run("json", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "list", f.EvalID, "-o", "json"))
		require.True(t, strings.HasPrefix(strings.TrimSpace(r.Stdout), "["),
			"a list must be a bare array, not the service's envelope")

		var runs []runSummary
		r.JSON(t, &runs)
		require.GreaterOrEqual(t, len(runs), 2)

		byID := map[string]runSummary{}
		for _, entry := range runs {
			byID[entry.ID] = entry
		}
		baseline, ok := byID[f.BaselineRunID]
		require.True(t, ok, "the eval's own run is missing from its listing")
		require.Equal(t, "completed", baseline.Status)
		require.NotNil(t, baseline.ResultCounts)
		require.Equal(t, 2, baseline.ResultCounts.Failed)
	})

	// The client has always taken a limit; until recently the command did not
	// expose one, so a service-side truncation would have passed unnoticed.
	t.Run("limit", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "list", f.EvalID, "--limit", "1", "-o", "json"))
		var runs []runSummary
		r.JSON(t, &runs)
		require.Len(t, runs, 1, "--limit must reach the service")
	})

	t.Run("unknown eval is brief", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "list", "eval_azdcli_no_such_eval"))
		require.Less(t, len(r.Combined()), 600,
			"a not-found must stay short, not dump the service body:\n%s", r.Combined())
		require.Contains(t, r.Combined(), "eval_azdcli_no_such_eval")
	})
}

func TestCLIRunShow(t *testing.T) {
	f := sharedEval(t)

	t.Run("by run id", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "show", f.EvalID, "--run-id", f.BaselineRunID))
		require.Contains(t, r.Stdout, f.BaselineRunID)
		require.Contains(t, r.Stdout, "status")
		require.Contains(t, r.Stdout, "completed")
		require.Contains(t, r.Stdout, "1 passed, 2 failed, 0 errored")
		require.Contains(t, r.Stdout, "report")
	})

	// Without --run-id the command has to pick one, and outside an azd
	// environment there is no remembered id to fall back on, so what is
	// exercised is the listing path.
	t.Run("defaults to the most recent run", func(t *testing.T) {
		listed := requireSuccess(t, run(t, "run", "list", f.EvalID, "--limit", "1", "-o", "json"))
		var newest []runSummary
		listed.JSON(t, &newest)
		require.Len(t, newest, 1)

		r := requireSuccess(t, run(t, "run", "show", f.EvalID, "-o", "json"))
		var shown runSummary
		r.JSON(t, &shown)
		require.Equal(t, newest[0].ID, shown.ID,
			"the default must be the run the listing puts first")
	})

	// A remembered run that no longer resolves falls through to the eval's
	// latest, but one named explicitly must not: silently showing a different
	// run than the one asked for is worse than saying it is gone.
	//
	// Only the substitution is asserted. Unlike `run list` and `run delete`,
	// this path does not shorten the service's body, so the message runs to
	// about 1700 characters of raw JSON — recorded in the report rather than
	// pinned here, since pinning it would make the length a requirement.
	t.Run("an unknown run id is reported, not silently replaced", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "show", f.EvalID, "--run-id", "evalrun_azdcli_nope"))
		require.Contains(t, r.Combined(), "evalrun_azdcli_nope",
			"the failure must name the run that was asked for")
		require.NotContains(t, r.Combined(), f.BaselineRunID,
			"an explicit --run-id must not fall back to another run")
	})
}

// TestCLIRunCancelAndDelete covers both halves of cancel, and the delete that
// follows it, against a single in-flight run: each run costs a minute of
// service time, so the two happy paths share one.
//
// The service answers a cancel on a finished run with success, so without the
// guard the command would tell a user it had stopped something it had not.
func TestCLIRunCancelAndDelete(t *testing.T) {
	f := sharedEval(t)

	t.Run("a finished run is refused", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "cancel", f.EvalID, "--run-id", f.BaselineRunID))
		require.Contains(t, r.Combined(), "already finished")
		require.Contains(t, r.Combined(), "completed")
	})

	// Delete is covered as far as the service honours it.
	//
	// The removal itself is not asserted, because it does not happen: the
	// service accepts the DELETE and the run is still readable by id and still
	// in the listing minutes later. What is asserted instead is that the
	// command reaches the right resource — a real run is accepted, an unknown
	// one is refused — which is the part that would break if the route or the
	// id handling regressed.
	t.Run("an in-flight run is cancelled, and the delete is accepted", func(t *testing.T) {
		runID := startCancellableRun(t, f)

		cancelled := requireSuccess(t, run(t, "run", "cancel", f.EvalID, "--run-id", runID))
		require.Contains(t, cancelled.Stdout, runID)
		require.Contains(t, cancelled.Stdout, "is now")

		shown := requireSuccess(t, run(t, "run", "show", f.EvalID, "--run-id", runID, "-o", "json"))
		var after runSummary
		shown.JSON(t, &after)
		require.NotEqual(t, "completed", after.Status,
			"a cancelled run must not go on to complete")

		deleted := requireSuccess(t, run(t, "run", "delete", f.EvalID, "--run-id", runID))
		require.Contains(t, deleted.Stdout, "Deleted run")
		require.Contains(t, deleted.Stdout, runID)

		still := requireSuccess(t, run(t, "run", "show", f.EvalID, "--run-id", runID, "-o", "json"))
		var survivor runSummary
		still.JSON(t, &survivor)
		t.Logf("the run is still readable after a successful delete (status %q); "+
			"the service accepts the request without removing anything", survivor.Status)
	})

	// Deleting is not undoable, so the id is required rather than defaulted to
	// whichever run happens to be newest.
	t.Run("delete requires the run id", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "delete", f.EvalID))
		require.Contains(t, r.Combined(), "--run-id is required")
	})

	t.Run("deleting an unknown run is reported briefly", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "delete", f.EvalID, "--run-id", "evalrun_azdcli_nope"))
		require.Contains(t, r.Combined(), "evalrun_azdcli_nope")
		require.Less(t, len(r.Combined()), 600,
			"a not-found must stay short, not dump the service body:\n%s", r.Combined())
	})
}

// startCancellableRun adds a run to the fixture's eval and returns it before it
// can finish.
//
// The rows are padded so the run cannot complete inside the second it takes to
// issue the cancel; a run that finished first would turn the cancel test into
// an assertion about the guard it is not testing.
func startCancellableRun(t *testing.T, f *evalFixture) string {
	t.Helper()

	client, err := liveClient()
	require.NoError(t, err)

	responses := make([]string, 0, 40)
	for i := range 40 {
		responses = append(responses, strings.Repeat("a good answer ", i%5+1))
	}

	runID, err := startFixtureRun(context.Background(), client, f.EvalID, "cancelme", responses)
	require.NoError(t, err, "starting a run to cancel")
	t.Cleanup(func() {
		_ = client.DeleteOpenAIEvalRun(context.Background(), f.EvalID, runID)
	})
	return runID
}
