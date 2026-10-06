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

// runPassRateValue is the one definition of a run's pass rate: the share of
// all test cases that passed.
//
// Total is the denominator so every non-passing terminal row counts against
// the run, including failed, errored, skipped, and future outcomes represented
// by the service total. Display and gating use this same calculation.
//
// ok is false for inconsistent counts or no test cases: callers must not
// display a rate from invalid operands or divide by zero.
func runPassRateValue(counts *eval_api.EvalRunResultCounts) (rate float64, total int, ok bool) {
	if !validRunPassRateCounts(counts) || counts.Total == 0 {
		return 0, 0, false
	}
	return float64(counts.Passed) / float64(counts.Total), counts.Total, true
}

func validRunPassRateCounts(counts *eval_api.EvalRunResultCounts) bool {
	return counts != nil && counts.Total >= 0 && counts.Passed >= 0 && counts.Passed <= counts.Total
}

// evaluate checks count presence before deciding whether the quality gate was
// breached. Missing operands are an operational error, not a quality verdict.
func (g gate) evaluate(run *eval_api.OpenAIEvalRun) (string, error) {
	if !g.set {
		return "", nil
	}
	var counts map[string]int
	if run != nil {
		counts = run.ReportedResultCounts()
	}
	if _, totalKnown := counts["total"]; totalKnown && !validRunPassRateCounts(run.ResultCounts) {
		return "", messages.GateCountsInvalid()
	}
	if total, reported := counts["total"]; reported && total == 0 {
		return messages.GateNoTestCases(), nil
	}
	required := []string{"total", "passed"}
	var missing []string
	for _, name := range required {
		if _, reported := counts[name]; !reported {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return "", messages.GateCountsUnavailable(missing)
	}
	return g.breach(run.ResultCounts), nil
}

// breach compares counts whose required operands evaluate has checked.
//
// A run with no test cases breaches every threshold rather than dividing by
// zero — "no rows passed" is the honest reading of an empty result.
func (g gate) breach(counts *eval_api.EvalRunResultCounts) string {
	if !g.set {
		return ""
	}
	if counts == nil {
		return messages.GateNoResultCounts()
	}
	if g.anyFailure {
		if counts.Total == 0 {
			return messages.GateNoTestCases()
		}
		// This is the pass-rate rule asked as a yes/no question: every row that
		// did not pass counts against the run, whatever its terminal outcome.
		unpassed := counts.Total - counts.Passed
		if unpassed > 0 {
			return messages.GateSamplesDidNotPass(unpassed, counts.Total)
		}
		return ""
	}
	actual, _, ok := runPassRateValue(counts)
	if !ok {
		return messages.GateNoTestCases()
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
// threshold, or returns an operational error when its counts are insufficient.
//
// It exits here rather than returning an error because the extension SDK's
// Run collapses every error to exit 1, and the whole point of the flag is a
// code a pipeline can tell apart from an operational failure.
func applyGate(cmd *cobra.Command, g gate, run *eval_api.OpenAIEvalRun) error {
	reason, err := g.evaluate(run)
	if err != nil {
		return err
	}
	if !g.set {
		return nil
	}
	// The service total is the denominator even when its outcome breakdown does
	// not account for every row. Warn about that mismatch without suggesting
	// those rows were excluded from the gate.
	if !g.anyFailure {
		if run != nil && run.ResultCounts != nil {
			counts := run.ReportedResultCounts()
			total, totalKnown := counts["total"]
			passed, passedKnown := counts["passed"]
			failed, failedKnown := counts["failed"]
			errored, erroredKnown := counts["errored"]
			skipped, skippedKnown := counts["skipped"]
			accounted := passed + failed + errored + skipped
			if totalKnown && passedKnown && failedKnown && erroredKnown && skippedKnown && total > accounted {
				fmt.Fprint(cmd.ErrOrStderr(),
					messages.Warning(messages.GateUnaccountedRows(total-accounted, total)))
			}
		}
	}
	if reason == "" {
		return nil
	}
	fmt.Fprint(cmd.ErrOrStderr(), gateBreachMessage(reason))
	os.Exit(exitCodeGateBreached)
	return nil
}

func addFailOnFlag(cmd *cobra.Command, target *string) {
	// States the observed code rather than the one this process exits with: azd
	// collapses an extension's exit code, and a pipeline author who reads 2 here
	// writes a condition that never fires.
	cmd.Flags().StringVar(target, "fail-on", "",
		"Fail when the run misses this threshold: any-failure, or pass-rate=<0..1>. "+
			"pass-rate is passed test cases divided by total test cases, so failed, "+
			"errored, skipped, and otherwise unpassed rows count against the run. "+
			"Exits 1.")
}
