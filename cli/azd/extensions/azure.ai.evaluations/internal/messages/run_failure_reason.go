// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import "fmt"

// RunFinishedWithReason reports a run that ended in something other than
// completed and says why, so the line a pipeline logs carries the reason and
// not only the status.
func RunFinishedWithReason(runID, status, reason string) error {
	return fmt.Errorf("run %s finished with status %s: %s", runID, status, reason)
}

// RunRowErrorsHeading introduces the distinct reasons evaluators gave for
// producing no verdict.
func RunRowErrorsHeading() string {
	return "\nEVALUATOR ERRORS\n"
}

// RunRowError is one distinct reason, with how many rows it affected. The
// evaluator and code are optional because a service may send neither.
func RunRowError(evaluator, code, message string, rows int) string {
	label := "evaluator"
	if evaluator != "" {
		label = evaluator
	}
	reason := message
	switch {
	case code != "" && message != "":
		reason = code + ": " + message
	case code != "":
		reason = code
	}
	noun := "rows"
	if rows == 1 {
		noun = "row"
	}
	return fmt.Sprintf("  %s: %s (%d %s)\n", label, reason, rows, noun)
}

// RunRowErrorsMore says how many further distinct reasons were left out.
func RunRowErrorsMore(hidden int) string {
	return fmt.Sprintf("  ... and %d more; the errored rows hold every reason\n", hidden)
}
