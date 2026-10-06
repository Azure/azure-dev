// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"fmt"
	"io"
	"slices"

	"azureaieval/internal/failuretext"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
)

const (
	// maxRowErrorGroups bounds how many distinct reasons a summary prints. A run
	// that errored for one reason on every row has one; the cap only matters when
	// the reasons differ, and then the errored rows hold the rest.
	maxRowErrorGroups = 3
	// maxFailureTextRunes bounds one reason so a service body pasted into an error
	// cannot bury the summary.
	maxFailureTextRunes = failuretext.MaxRunes
)

// rowErrorGroup is one distinct reason an evaluator gave for scoring no verdict
// on a row, and how many rows it affected.
type rowErrorGroup struct {
	evaluator, code, message string
	rows                     int
}

// summarizeRowErrors collects why evaluators errored, from the rows themselves.
//
// A run-level error is often empty when every row errored: the reason lives on
// each evaluator's sample, one per row. Reasons are grouped by what a reader
// would see -- after redaction and one-line bounding -- so two messages that
// differ only in a part that is not printed are one reason, and the most common
// reason comes first so the cap on printed lines never hides the dominant one.
func summarizeRowErrors(items []eval_api.OutputItem) []rowErrorGroup {
	var groups []rowErrorGroup
	index := map[rowErrorGroup]int{}
	for _, item := range items {
		counted := map[rowErrorGroup]bool{}
		for _, result := range item.Results {
			sample := result.SampleError()
			if sample == nil {
				continue
			}
			key := rowErrorGroup{
				evaluator: failureText(result.Name),
				code:      failureText(sample.Code),
				message:   failureText(sample.Message),
			}
			if counted[key] {
				continue
			}
			counted[key] = true
			if at, seen := index[key]; seen {
				groups[at].rows++
				continue
			}
			index[key] = len(groups)
			key.rows = 1
			groups = append(groups, key)
		}
	}
	slices.SortStableFunc(groups, func(a, b rowErrorGroup) int { return cmp.Compare(b.rows, a.rows) })
	return groups
}

// renderRowErrors prints the distinct reasons evaluators errored, redacted the
// same way the run-level reason is.
func renderRowErrors(out io.Writer, groups []rowErrorGroup) {
	if len(groups) == 0 {
		return
	}
	fmt.Fprint(out, messages.RunRowErrorsHeading())
	for i, group := range groups {
		if i == maxRowErrorGroups {
			fmt.Fprint(out, messages.RunRowErrorsMore(len(groups)-maxRowErrorGroups))
			break
		}
		fmt.Fprint(out, messages.RunRowError(
			failureText(group.evaluator), failureText(group.code), failureText(group.message), group.rows))
	}
}

// writeJobFailure prints why a generation job failed, redacted like every other
// service-supplied reason. Nested explanations can quote download URLs, so the
// text is no longer printed as it arrived.
func writeJobFailure(out io.Writer, job *eval_api.GenerationJob) {
	if job == nil {
		return
	}
	if reason := job.Error.Reason(); reason != "" {
		fmt.Fprint(out, messages.JobErrorLine(failureText(reason)))
		renderFailureDetails(out, job.Error)
	}
}

// failureText makes a service-supplied reason safe to print on one line: URL
// credentials are redacted, whitespace is collapsed, and length is bounded.
func failureText(text string) string {
	return failuretext.Text(text)
}
