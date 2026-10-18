// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"strings"

	"azureaieval/internal/pkg/eval_api"
)

type conversationOutputSummary struct {
	complete     bool
	rows         int
	identified   int
	completed    int
	failed       int
	errored      int
	other        int
	unidentified int
}

// summarizeConversationOutput must only receive an unfiltered, complete listing.
// Item lifecycle states describe output processing, not simulation completion or
// evaluator verdicts. Conflicting states for one ID remain unknown.
func summarizeConversationOutput(items []eval_api.OutputItem) *conversationOutputSummary {
	summary := &conversationOutputSummary{complete: true, rows: len(items)}
	statuses := make(map[string]string)
	for _, item := range items {
		id, ok := item.DataSourceItem["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			summary.unidentified++
			continue
		}
		status := strings.ToLower(strings.TrimSpace(item.Status))
		if previous, exists := statuses[id]; exists && previous != status {
			status = "mixed"
		}
		statuses[id] = status
	}
	summary.identified = len(statuses)
	for _, status := range statuses {
		switch status {
		case "completed":
			summary.completed++
		case "failed":
			summary.failed++
		case "errored", "error":
			summary.errored++
		default:
			summary.other++
		}
	}
	return summary
}

func renderConversationOutput(out io.Writer, summary *conversationOutputSummary) {
	fmt.Fprint(out, "\nOBSERVED CONVERSATION OUTPUT\n")
	if !summary.complete {
		fmt.Fprint(out, "Not available: the complete output listing could not be read.\n")
		return
	}
	fmt.Fprintf(out, "Across all %d returned output rows, deduplicated by conversation ID:\n", summary.rows)
	for _, row := range []struct {
		label string
		count int
	}{
		{"Identified conversation IDs", summary.identified},
		{"IDs with completed output", summary.completed},
		{"IDs with failed output", summary.failed},
		{"IDs with errored output", summary.errored},
		{"IDs with other/mixed status", summary.other},
		{"Rows without conversation ID", summary.unidentified},
	} {
		fmt.Fprintf(out, "%-30s %d\n", row.label, row.count)
	}
	fmt.Fprint(out, "Output status is not a generation count, conversation-completion count, or quality verdict.\n")
}
