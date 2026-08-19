// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

// Gating is opt-in. A completed run with failing samples exits 0 without
// --fail-on: failing samples are the expected output of a working evaluation,
// not a tool error, and `run start` is used constantly in the inner loop. A
// default that returned non-zero on any failure would break a build the first
// time a noisy grader disagreed.
//
// The separate exit code matters more than the flag. It lets a pipeline tell
// "the evaluation regressed" from "the evaluation could not run", which are
// different failures with different owners.

// exitCodeGateBreached is returned when a run completed but missed its
// threshold.
const exitCodeGateBreached = 2

// gate is a parsed --fail-on threshold.
type gate struct {
	set        bool
	anyFailure bool
	passRate   float64
}

// parseGate reads the --fail-on value. An empty value means no gating.
func parseGate(spec string) (gate, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return gate{}, nil
	}
	if spec == "any-failure" {
		return gate{set: true, anyFailure: true}, nil
	}

	rate, ok := strings.CutPrefix(spec, "pass-rate=")
	if !ok {
		return gate{}, messages.FailOnInvalid(spec)
	}
	value, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return gate{}, messages.FailOnRateNotNumber(rate)
	}
	// NaN parses, then passes both range checks, and then loses every
	// comparison it is put in -- so a pipeline that asked to be gated would
	// never be, and nothing would say so.
	if math.IsNaN(value) {
		return gate{}, messages.FailOnRateNotNumber(rate)
	}
	if value < 0 || value > 1 {
		return gate{}, messages.FailOnRateOutOfRange(value)
	}
	return gate{set: true, passRate: value}, nil
}

// scoredPassRate is the one definition of a run's pass rate: the share of the
// rows an evaluator actually scored.
//
// Errored and skipped rows are outside the denominator because nothing graded
// them, and an infrastructure failure is not a quality signal. This is what the
// portal reports and what `--fail-on pass-rate` compares against, so the two
// figures a reader sees two lines apart cannot disagree.
//
// ok is false when nothing was scored at all: a rate over no rows is not zero,
// it is absent, and the caller has to say so rather than print it.
//
// The consequence is worth stating. A run where almost everything errored can
// now report a high rate off the few rows that survived, so the count that did
// not score is printed beside it.
func scoredPassRate(counts *eval_api.EvalRunResultCounts) (rate float64, scored int, ok bool) {
	if counts == nil {
		return 0, 0, false
	}
	scored = counts.Passed + counts.Failed
	if scored <= 0 {
		return 0, 0, false
	}
	return float64(counts.Passed) / float64(scored), scored, true
}

// breach reports why the run missed the threshold, or empty when it met it.
//
// A run that scored nothing at all breaches every threshold rather than
// dividing by zero — "no rows passed" is the honest reading of an empty result.
func (g gate) breach(counts *eval_api.EvalRunResultCounts) string {
	if !g.set {
		return ""
	}
	if counts == nil {
		return messages.GateNoResultCounts()
	}
	// Checked before any-failure as well as before the rate: a run that graded
	// nothing has not passed, and reading zero unpassed rows as success let an
	// empty run clear the gate that exists to catch exactly that.
	if counts.Total == 0 {
		return messages.GateNoRowsScored()
	}
	if g.anyFailure {
		// Deliberately stricter than the rate: this counts a row nothing could
		// grade against the run, because "everything passed" is not true of a
		// run that failed to grade half of what it was given.
		unpassed := counts.Total - counts.Passed
		if unpassed > 0 {
			return messages.GateSamplesDidNotPass(unpassed, counts.Total)
		}
		return ""
	}
	actual, _, ok := scoredPassRate(counts)
	if !ok {
		return messages.GateNoRowsScored()
	}
	if actual < g.passRate {
		return messages.GatePassRateBelow(actual, g.passRate)
	}
	return ""
}

// gateBreachMessage is what a breached gate prints, kept separate from the
// exit so the wording can be tested: it is the block the spec's CI scenario
// shows, and a pipeline's logs are where it is read.
func gateBreachMessage(reason string) string {
	return messages.GateBreached(reason)
}

// applyGate ends the process with exit code 2 when the run missed its
// threshold.
//
// It exits here rather than returning an error because the extension SDK's
// Run collapses every error to exit 1, and the whole point of the flag is a
// code a pipeline can tell apart from an operational failure.
func applyGate(cmd *cobra.Command, g gate, run *eval_api.OpenAIEvalRun) {
	if run == nil {
		return
	}
	// Rows nothing could grade are outside the rate, so a run that errored on
	// most of what it was given can clear a threshold on the few that survived.
	// That is the cost of measuring quality over scored rows only, and the gate
	// is where it has to be said: this is the line a pipeline log keeps.
	if g.set && !g.anyFailure {
		if c := run.ResultCounts; c != nil {
			if _, scored, ok := scoredPassRate(c); ok && c.Total > scored {
				fmt.Fprint(os.Stderr,
					messages.Warning(messages.GateSawUnscoredRows(c.Total-scored, c.Total)))
			}
		}
	}
	reason := g.breach(run.ResultCounts)
	if reason == "" {
		return
	}
	fmt.Fprint(os.Stderr, gateBreachMessage(reason))
	os.Exit(exitCodeGateBreached)
}

func addFailOnFlag(cmd *cobra.Command, target *string) {
	// States the observed code rather than the one this process exits with: azd
	// collapses an extension's exit code, and a pipeline author who reads 2 here
	// writes a condition that never fires.
	cmd.Flags().StringVar(target, "fail-on", "",
		"Fail when the run misses this threshold: any-failure, or pass-rate=<0..1>. "+
			"pass-rate is measured over the rows that were scored, so rows nothing "+
			"could grade are outside it; any-failure counts them against the run. "+
			"Exits 1.")
}
