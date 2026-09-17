// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"math"
	"slices"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// traceWindowDays are the windows offered, in the order asked.
//
// A custom range is deliberately absent: the three that matter are a day, a
// week and a month, and a free-text duration is a fourth thing to get wrong at
// the one prompt where being wrong is invisible. `lookback_hours` in the file
// remains the way to say anything else.
var traceWindowDays = []int{1, 7, 30}

// defaultTraceWindowDays is the week the spec writes as lookback_hours: 168.
const defaultTraceWindowDays = 7

// resolveTraceWindow settles how far back a trace-backed eval reads, in hours.
//
// init wrote no lookback at all, so the window was whatever the service chose
// on the day it ran -- the one setting that decides what the eval measured, and
// the only one the file did not record.
func resolveTraceWindow(cmd *cobra.Command, flagDays int, flagGiven bool) (int, error) {
	if flagGiven {
		if !slices.Contains(traceWindowDays, flagDays) {
			return 0, messages.TraceDaysNotAChoice(flagDays, traceWindowDays)
		}
		return flagDays * 24, nil
	}
	if noPrompt(cmd) {
		return defaultTraceWindowDays * 24, nil
	}
	return promptTraceWindow(cmd)
}

// promptTraceWindow asks how far back to read, a week preselected.
func promptTraceWindow(cmd *cobra.Command) (int, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return 0, messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	choices := make([]*azdext.SelectChoice, 0, len(traceWindowDays))
	selected := 0
	for i, days := range traceWindowDays {
		if days == defaultTraceWindowDays {
			selected = i
		}
		choices = append(choices, &azdext.SelectChoice{
			Label: messages.TraceWindowChoice(days),
			Value: messages.TraceWindowChoice(days),
		})
	}

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         messages.SelectTraceWindowPrompt(),
			Choices:         choices,
			SelectedIndex:   preselect(selected),
			EnableFiltering: filteringFor(len(choices)),
		},
	})
	if err != nil {
		return 0, messages.SelectingTraceWindow(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from GetValue
	// and would read as "last 24 hours" -- a tenth of the window the prompt was
	// showing as chosen. An unanswered prompt means the default it displayed.
	if resp == nil || resp.Value == nil {
		return defaultTraceWindowDays * 24, nil
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(traceWindowDays) {
		return defaultTraceWindowDays * 24, nil
	}
	return traceWindowDays[index] * 24, nil
}

// preselect is the wire's index, or nil where it cannot be represented.
//
// Guarded rather than converted: an index that does not fit wraps to some other
// choice, and a prompt that highlights the wrong default is worse than one that
// highlights none.
func preselect(i int) *int32 {
	if i < 0 || i > math.MaxInt32 {
		return nil
	}
	n := int32(i)
	return &n
}

// filterAbove is the number of choices a picker has to exceed before it offers
// a search row.
//
// A filter over a list you can already see is furniture: it costs a line, it
// invites typing where an arrow key would do, and on a two-option Traces or
// Dataset picker it suggests there is more to find.
const filterAbove = 5

// filteringFor answers whether a picker of this size should offer filtering.
//
// Returned as the wire's optional bool so the decision is explicit at every
// picker rather than left to whatever the host defaults to.
func filteringFor(choices int) *bool {
	on := choices > filterAbove
	return &on
}
