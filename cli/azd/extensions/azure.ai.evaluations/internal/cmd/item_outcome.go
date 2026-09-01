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
	Passed   int
	Failed   int
	Unscored int
	// Attention names only the results not passing, which is the column the
	// old EVALUATORS heading described inaccurately.
	Attention []string
	Reason    string
}

// Total is the number of criterion results this item carries.
func (o itemOutcome) Total() int { return o.Passed + o.Failed + o.Unscored }

// classifyItem derives an item's outcome from its evaluator results.
//
// The service's own item status cannot carry this: a run whose samples all
// errored still reports every item as `completed`, with the failure visible
// only as a null score and a null verdict on each result.
func classifyItem(item eval_api.OutputItem) itemOutcome {
	out := itemOutcome{Status: itemPassed}

	// A failing verdict explains the row; a passing one explains only the
	// score, so a failure is never displaced by whichever result came first.
	failedReason, anyReason := "", ""

	for _, r := range item.Results {
		switch {
		case !r.Judged():
			out.Unscored++
			out.Attention = append(out.Attention, r.Name+": no verdict")
		case r.DidPass():
			out.Passed++
		default:
			out.Failed++
			out.Attention = append(out.Attention, r.Name+": failed")
			if failedReason == "" {
				failedReason = r.Reason
			}
		}
		if anyReason == "" {
			anyReason = r.Reason
		}
	}

	switch {
	case out.Failed > 0:
		out.Status = itemFailed
	case out.Unscored > 0 && out.Passed == 0:
		// Nothing about this row was measured, so calling it passed would
		// report an infrastructure failure as a quality result.
		out.Status = itemErrored
	case out.Total() == 0:
		out.Status = itemSkipped
	}

	out.Reason = failedReason
	if out.Reason == "" {
		out.Reason = anyReason
	}
	return out
}

// ResultsBreakdown renders the item's criterion results as P/F/U counts, so a
// row states how much of it was actually judged.
func (o itemOutcome) ResultsBreakdown() string {
	if o.Total() == 0 {
		return "-"
	}
	if o.Unscored == 0 {
		return fmt.Sprintf("%dP/%dF", o.Passed, o.Failed)
	}
	return fmt.Sprintf("%dP/%dF/%dU", o.Passed, o.Failed, o.Unscored)
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
