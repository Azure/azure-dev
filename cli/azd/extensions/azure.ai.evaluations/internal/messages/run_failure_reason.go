// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"fmt"
	"strings"
)

// RunFinishedWithReason reports a run that ended in something other than
// completed and says why, so the line a pipeline logs carries the reason and
// not only the status.
func RunFinishedWithReason(runID, status, reason string) error {
	return fmt.Errorf("run %s finished with status %s: %s", runID, status, reason)
}

// FailureDetailWithTarget names what a failure detail is about.
func FailureDetailWithTarget(detail, target string) string {
	return fmt.Sprintf("%s (target: %s)", detail, target)
}

// FailureDetail is one specific finding listed under a failure's headline.
func FailureDetail(detail string) string {
	return fmt.Sprintf("  - %s\n", detail)
}

// FailureDetailsMore says how many further findings were left out.
func FailureDetailsMore(hidden int) string {
	return fmt.Sprintf("  ... and %d more not shown\n", hidden)
}

// FailureReasonWithDetails appends a failure's findings to its headline for a
// single-line report.
func FailureReasonWithDetails(headline string, details []string, hidden int) string {
	if len(details) == 0 {
		return headline
	}
	list := strings.Join(details, "; ")
	if hidden > 0 {
		list += fmt.Sprintf("; and %d more", hidden)
	}
	return fmt.Sprintf("%s (details: %s)", headline, list)
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
