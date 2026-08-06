// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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
		return gate{}, fmt.Errorf(
			"--fail-on must be any-failure or pass-rate=<0..1>, got %q", spec)
	}
	value, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return gate{}, fmt.Errorf("--fail-on pass-rate must be a number, got %q", rate)
	}
	if value < 0 || value > 1 {
		return gate{}, fmt.Errorf("--fail-on pass-rate must be between 0 and 1, got %v", value)
	}
	return gate{set: true, passRate: value}, nil
}

// breach reports why the run missed the threshold, or empty when it met it.
//
// Errored and skipped rows count against the pass rate, and they can: the
// service puts them inside `total`, verified live on a run that reported
// total=3 passed=2 errored=1. Were they outside it, a run with two passes and
// one error would report total=2 and score a perfect rate, which is precisely
// the broken evaluation a gate exists to catch.
//
// A run that scored nothing at all breaches every threshold rather than
// dividing by zero — "no rows passed" is the honest reading of an empty result.
func (g gate) breach(counts *eval_api.EvalRunResultCounts) string {
	if !g.set {
		return ""
	}
	if counts == nil {
		return "the run reported no result counts, so the threshold cannot be checked"
	}
	if g.anyFailure {
		unpassed := counts.Total - counts.Passed
		if unpassed > 0 {
			return fmt.Sprintf("%d of %d samples did not pass", unpassed, counts.Total)
		}
		return ""
	}
	if counts.Total == 0 {
		return "the run scored no rows, so its pass rate is below any threshold"
	}
	actual := float64(counts.Passed) / float64(counts.Total)
	if actual < g.passRate {
		return fmt.Sprintf("pass rate %.1f%% is below the required %.1f%%",
			actual*100, g.passRate*100)
	}
	return ""
}

// gateBreachMessage is what a breached gate prints, kept separate from the
// exit so the wording can be tested: it is the block the spec's CI scenario
// shows, and a pipeline's logs are where it is read.
func gateBreachMessage(reason string) string {
	return fmt.Sprintf("%s Evaluation gate: %s\n\nERROR: evaluation quality gate not met.\n",
		failedMark, reason)
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
	reason := g.breach(run.ResultCounts)
	if reason == "" {
		return
	}
	fmt.Fprint(os.Stderr, gateBreachMessage(reason))
	os.Exit(exitCodeGateBreached)
}

func addFailOnFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "fail-on", "",
		"Exit 2 when the run misses this threshold: any-failure, or pass-rate=<0..1>.")
}
