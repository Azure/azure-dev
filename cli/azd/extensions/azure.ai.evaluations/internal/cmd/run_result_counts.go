// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"strconv"

	"azureaieval/internal/pkg/eval_api"
)

func resultCountText(counts map[string]int, name string) string {
	if count, ok := counts[name]; ok {
		return strconv.Itoa(count)
	}
	return "not reported"
}

func renderReportedRunCounts(out io.Writer, heading string, counts map[string]int) {
	fmt.Fprintf(out, "\n%s\n", heading)
	if len(counts) == 0 {
		fmt.Fprint(out, "Not reported by the service.\n")
		return
	}
	for _, row := range []field{
		{"Total", "total"}, {"Passed", "passed"}, {"Failed", "failed"},
		{"Errored", "errored"}, {"Skipped", "skipped"},
	} {
		fmt.Fprintf(out, "%-10s %4s\n", row.Key, resultCountText(counts, row.Value))
	}
	passed, passedKnown := counts["passed"]
	failed, failedKnown := counts["failed"]
	if passedKnown && failedKnown {
		fmt.Fprintf(out, "%-10s %s (%d passed / (%d passed + %d failed))\n",
			"Pass rate", formatRate(passed, passed+failed), passed, passed, failed)
	} else {
		fmt.Fprint(out, "Pass rate  not reported\n")
	}
}

func reportedSampleCount(run *eval_api.OpenAIEvalRun) string {
	if run == nil || run.ResultCounts == nil {
		return ""
	}
	if _, reported := run.ReportedResultCounts()["total"]; !reported {
		return "not reported"
	}
	return sampleCount(run.ResultCounts)
}

func reportedRunPassRate(run *eval_api.OpenAIEvalRun) string {
	if run.ResultCounts == nil {
		return ""
	}
	counts := run.ReportedResultCounts()
	_, passedKnown := counts["passed"]
	_, failedKnown := counts["failed"]
	if !passedKnown || !failedKnown {
		return "not reported"
	}
	return runPassRate(run.ResultCounts)
}
