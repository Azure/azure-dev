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
		r := requireSuccess(t, run(t, "run", "list", "--eval", f.EvalID))
		// These are the columns the spec's `run list` sample prints, in the
		// spec's own wording. The old RUN ID/NAME/RESULTS set predates it.
		for _, header := range []string{
			"RUN", "DATASET", "STARTED", "STATUS", "SAMPLES", "PASS RATE",
		} {
			require.Containsf(t, r.Stdout, header, "the listing lost its %s column", header)
		}
		require.Contains(t, r.Stdout, f.FirstRunID)
		require.Contains(t, r.Stdout, f.SecondRunID)
		require.Regexp(t, `\d+\.\d+%`, r.Stdout,
			"the listing must summarise each run's pass rate, not just its status")
	})

	t.Run("json", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "list", "--eval", f.EvalID, "-o", "json"))
		require.True(t, strings.HasPrefix(strings.TrimSpace(r.Stdout), "["),
			"a list must be a bare array, not the service's envelope")

		var runs []runSummary
		r.JSON(t, &runs)
		require.GreaterOrEqual(t, len(runs), 2)

		byID := map[string]runSummary{}
		for _, entry := range runs {
			byID[entry.ID] = entry
		}
		first, ok := byID[f.FirstRunID]
		require.True(t, ok, "the eval's own run is missing from its listing")
		require.Equal(t, "completed", first.Status)
		require.NotNil(t, first.ResultCounts)
		require.Equal(t, len(fixtureQueries),
			first.ResultCounts.Passed+first.ResultCounts.Failed,
			"every dataset row must be accounted for by a verdict")
	})

	// The client has always taken a limit; until recently the command did not
	// expose one, so a service-side truncation would have passed unnoticed.
	t.Run("limit", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "list", "--eval", f.EvalID, "--limit", "1", "-o", "json"))
		var runs []runSummary
		r.JSON(t, &runs)
		require.Len(t, runs, 1, "--limit must reach the service")
	})

	t.Run("unknown eval is brief", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "list", "--eval", "eval_azdcli_no_such_eval"))
		require.Less(t, len(r.Combined()), 600,
			"a not-found must stay short, not dump the service body:\n%s", r.Combined())
		require.Contains(t, r.Combined(), "eval_azdcli_no_such_eval")
	})
}

func TestCLIRunShow(t *testing.T) {
	f := sharedEval(t)

	t.Run("by run id", func(t *testing.T) {
		r := requireSuccess(t, run(t, "run", "show", f.FirstRunID, "--eval", f.EvalID))
		require.Contains(t, r.Stdout, f.FirstRunID)
		require.Contains(t, r.Stdout, "status")
		require.Contains(t, r.Stdout, "completed")
		require.Regexp(t, `\d+ passed, \d+ failed, \d+ errored`, r.Stdout)
		require.Contains(t, r.Stdout, "report")
	})

	// Without --run-id the command has to pick one, and outside an azd
	// environment there is no remembered id to fall back on, so what is
	// exercised is the listing path.
	t.Run("defaults to the most recent run", func(t *testing.T) {
		listed := requireSuccess(t, run(t, "run", "list", "--eval", f.EvalID, "--limit", "1", "-o", "json"))
		var newest []runSummary
		listed.JSON(t, &newest)
		require.Len(t, newest, 1)

		r := requireSuccess(t, run(t, "run", "show", "--eval", f.EvalID, "-o", "json"))
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
		r := requireFailure(t, run(t, "run", "show", "evalrun_azdcli_nope", "--eval", f.EvalID))
		require.Contains(t, r.Combined(), "evalrun_azdcli_nope",
			"the failure must name the run that was asked for")
		require.NotContains(t, r.Combined(), f.FirstRunID,
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
		r := requireFailure(t, run(t, "run", "cancel", f.FirstRunID, "--eval", f.EvalID))
		require.Contains(t, r.Combined(), "already finished")
		require.Contains(t, r.Combined(), "completed")
	})

	// Delete is covered as far as the service honours it.
	//
	// The removal itself is not asserted, because whether it happens is the
	// service's to decide and it has changed under this test once already: it
	// used to accept the DELETE and leave the run readable minutes later, and it
	// now reaps a cancelled run promptly enough that the DELETE can even answer
	// 404. What is asserted is that the command reaches the right resource — a
	// real run is accepted, an unknown one is refused — which is the part that
	// would break if the route or the id handling regressed.
	t.Run("an in-flight run is cancelled, and the delete is accepted", func(t *testing.T) {
		runID := startCancellableRun(t, f)

		cancelled := requireSuccess(t, run(t, "run", "cancel", runID, "--eval", f.EvalID))
		require.Contains(t, cancelled.Stdout, runID)
		require.Contains(t, cancelled.Stdout, "is now")

		shown := requireSuccess(t, run(t, "run", "show", runID, "--eval", f.EvalID, "-o", "json"))
		var after runSummary
		shown.JSON(t, &after)
		require.NotEqual(t, "completed", after.Status,
			"a cancelled run must not go on to complete")

		deleted := requireSuccess(t, run(t, "run", "delete", runID, "--eval", f.EvalID))
		require.Contains(t, deleted.Stdout, "Deleted run")
		require.Contains(t, deleted.Stdout, runID)

		// Either outcome is the service's prerogative; what matters is that the
		// answer is about this run and not a failure of some other kind.
		still := run(t, "run", "show", runID, "--eval", f.EvalID, "-o", "json")
		if still.ExitCode == 0 {
			var survivor runSummary
			still.JSON(t, &survivor)
			t.Logf("still readable after delete (status %q); the service accepted "+
				"the request without removing anything", survivor.Status)
		} else {
			require.Contains(t, still.Combined(), runID,
				"a read of a deleted run must still name the run it could not find")
			t.Log("gone after delete; the service removed it")
		}
	})

	// Deleting is not undoable, so the id is required rather than defaulted to
	// whichever run happens to be newest.
	t.Run("delete requires the run id", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "delete", "--eval", f.EvalID))
		require.Contains(t, r.Combined(), "accepts 1 arg")
	})

	t.Run("deleting an unknown run is reported briefly", func(t *testing.T) {
		r := requireFailure(t, run(t, "run", "delete", "evalrun_azdcli_nope", "--eval", f.EvalID))
		require.Contains(t, r.Combined(), "evalrun_azdcli_nope")
		require.Less(t, len(r.Combined()), 600,
			"a not-found must stay short, not dump the service body:\n%s", r.Combined())
	})
}

// startCancellableRun adds a run to the fixture's eval and returns it before it
// can finish.
//
// An agent-target run invokes the agent once per row and is judged after that,
// which takes far longer than the second it takes to issue the cancel; a run
// that finished first would turn the cancel test into an assertion about the
// guard it is not testing.
func startCancellableRun(t *testing.T, f *evalFixture) string {
	t.Helper()

	client, err := liveClient()
	require.NoError(t, err)

	runID, err := startFixtureRun(context.Background(), client, f.EvalID, f.AgentName, "cancelme")
	require.NoError(t, err, "starting a run to cancel")
	t.Cleanup(func() {
		_ = client.DeleteOpenAIEvalRun(context.Background(), f.EvalID, runID)
	})
	return runID
}
