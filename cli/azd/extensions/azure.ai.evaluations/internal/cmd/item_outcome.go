// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"slices"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
)

// Item outcomes, named as the service names them.
const (
	itemPassed  = "passed"
	itemFailed  = "failed"
	itemErrored = "errored"
	itemSkipped = "skipped"
)

// itemStatuses is the set --status accepts, in the order the summary reports.
var itemStatuses = []string{itemPassed, itemFailed, itemErrored, itemSkipped}

// itemOutcome is what one evaluated row amounts to across its evaluators.
//
// The listing used to encode this: a dash meant every evaluator passed, an
// evaluator's name meant that one failed, and "(no verdict)" meant nothing
// scored it. A reader had to learn three conventions to answer "did this row
// pass", and two of them looked like data rather than status.
type itemOutcome struct {
	Status string
	// Counts of criterion results for this item, which is what makes an item
	// row reconcile against the evaluator table above it.
	//
	// Skipped and Errored are separate because the service says which it was: a
	// row it deliberately skipped is not an infrastructure failure, and one
	// combined "unscored" count reported both as neither.
	Passed  int
	Failed  int
	Skipped int
	Errored int
	// Attention names only the results not passing, which is the column the
	// old EVALUATORS heading described inaccurately.
	Attention []string
	Reason    string
}

// Total is the number of criterion results this item carries.
func (o itemOutcome) Total() int { return o.Passed + o.Failed + o.Skipped + o.Errored }

// classifyItem derives an item's outcome from its evaluator results.
//
// The service's own item status is preserved when it is one of the four
// outcomes. It is often lifecycle-only: a run whose samples all errored still
// reports every item as `completed`, with the failure visible only on each
// result. So `completed` is derived from the results rather than shown.
func classifyItem(item eval_api.OutputItem) itemOutcome {
	out := itemOutcome{}

	// A failing verdict explains the row; a passing one explains only the
	// score, so a failure is never displaced by whichever result came first.
	failedReason, anyReason := "", ""

	for _, r := range item.Results {
		switch r.Outcome() {
		case eval_api.ResultPassed:
			out.Passed++
		case eval_api.ResultFailed:
			out.Failed++
			out.Attention = append(out.Attention, r.Name+": failed")
			if failedReason == "" {
				failedReason = r.Reason
			}
		case eval_api.ResultSkipped:
			out.Skipped++
			out.Attention = append(out.Attention, r.Name+": skipped")
		default:
			out.Errored++
			out.Attention = append(out.Attention, r.Name+": errored")
		}
		if anyReason == "" {
			anyReason = r.Reason
		}
	}

	out.Status = itemStatusFor(item.Status, out)
	out.Reason = failedReason
	if out.Reason == "" {
		out.Reason = anyReason
	}
	return out
}

// itemStatusFor keeps a canonical service status and derives the rest.
//
// The order is the contract's: one failed result decides the row, then one
// errored, then one passed. A row with nothing but skips is skipped, which is
// what stops a deliberate skip reading as a failure to run.
func itemStatusFor(serviceStatus string, o itemOutcome) string {
	if s := strings.ToLower(strings.TrimSpace(serviceStatus)); slices.Contains(itemStatuses, s) {
		return s
	}
	switch {
	case o.Failed > 0:
		return itemFailed
	case o.Errored > 0:
		return itemErrored
	case o.Passed > 0:
		return itemPassed
	default:
		return itemSkipped
	}
}

// ResultsBreakdown names the item's criterion results in full words.
//
// `2P/1F/1U` needed a legend, and its U combined a skip with a failure to run.
func (o itemOutcome) ResultsBreakdown() string {
	if o.Total() == 0 {
		return "-"
	}
	parts := make([]string, 0, 4)
	for _, c := range []struct {
		n    int
		word string
	}{
		{o.Passed, "passed"},
		{o.Failed, "failed"},
		{o.Skipped, "skipped"},
	} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.word))
		}
	}
	if o.Errored > 0 {
		// "errored" reads as a verdict beside "passed"; the count is of errors.
		word := "errors"
		if o.Errored == 1 {
			word = "error"
		}
		parts = append(parts, fmt.Sprintf("%d %s", o.Errored, word))
	}
	return strings.Join(parts, ", ")
}

// AttentionText lists the results worth looking at, kept to a cell.
//
// A row evaluated by forty evaluators must not widen the table, so the names
// past the first few collapse into a count and `run output show` carries the
// rest.
func (o itemOutcome) AttentionText(max int) string {
	if len(o.Attention) == 0 {
		return "-"
	}
	if len(o.Attention) <= max {
		return strings.Join(o.Attention, ", ")
	}
	shown := strings.Join(o.Attention[:max], ", ")
	return fmt.Sprintf("%s, +%d more", shown, len(o.Attention)-max)
}

// parseStatusFilter reads --status into the set of outcomes to keep.
func parseStatusFilter(raw string) (map[string]bool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	keep := map[string]bool{}
	for part := range strings.SplitSeq(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if !slices.Contains(itemStatuses, name) {
			return nil, messages.UnknownItemStatus(name, itemStatuses)
		}
		keep[name] = true
	}
	if len(keep) == 0 {
		return nil, nil
	}
	return keep, nil
}
