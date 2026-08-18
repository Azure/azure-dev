// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"time"

	"azureaieval/internal/messages"
)

// MaxLookbackHours bounds `lookback_hours` at ten years.
//
// Ten years is a policy bound, not an arithmetic one: the hours become a
// time.Duration in nanoseconds, which does not overflow until about 2,562,047
// hours. The tighter bound is here because a lookback in that range is a typo
// rather than a window, and the run it produces is expensive and empty.
const MaxLookbackHours = 24 * 365 * 10

// ResolveTraceWindow reads the span of traces an eval grades.
//
// One definition, called by the configuration check and again when the request
// is built. Two copies drifted apart on every axis they were not both tested
// on: the config refused a bound the request then dropped, and the request
// accepted values the config had already refused, so which rules applied
// depended on which door the eval came through.
//
// The window is resolved as well as checked, because a rule about a window can
// only be stated once the window is known, and both callers need the answer.
//
// A zero start or end means unbounded at that end.
func ResolveTraceWindow(source *SourceDecl) (start, end time.Time, err error) {
	if source == nil {
		return time.Time{}, time.Time{}, nil
	}

	// Parsed first, so a file that is wrong in two ways names the value that
	// cannot be read at all rather than the pair it also got wrong.
	start, err = traceBound("start_time", source.StartTime)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err = traceBound("end_time", source.EndTime)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if source.LookbackHours < 0 {
		return time.Time{}, time.Time{}, messages.NegativeLookbackHours(source.LookbackHours)
	}
	if source.LookbackHours > MaxLookbackHours {
		return time.Time{}, time.Time{}, messages.LookbackTooLarge(source.LookbackHours, MaxLookbackHours)
	}
	if source.MaxTraces < 0 {
		return time.Time{}, time.Time{}, messages.MaxTracesUnusable(source.MaxTraces)
	}
	// Two ways of saying where the window opens, and the file cannot say which
	// was meant. Every other contradictory pair here is refused rather than
	// ranked.
	if source.StartTime != "" && source.LookbackHours != 0 {
		return time.Time{}, time.Time{}, messages.TraceWindowOverSpecified()
	}

	// The lookback is measured back from where the window closes, which is now
	// when nothing closed it. Measuring from now regardless made the window a
	// function of the clock: `lookback_hours` beside an `end_time` validated
	// today and failed tomorrow, with the file unchanged.
	if start.IsZero() && source.LookbackHours > 0 {
		from := end
		if from.IsZero() {
			from = time.Now()
		}
		return from.Add(-time.Duration(source.LookbackHours) * time.Hour), end, nil
	}

	if !start.IsZero() && !end.IsZero() && !end.After(start) {
		return time.Time{}, time.Time{}, messages.TraceWindowEndsBeforeItStarts(
			source.StartTime, source.EndTime)
	}
	return start, end, nil
}

// traceBound reads one end of the window.
//
// A bound at or before the Unix epoch is refused rather than returned. Zero is
// what every layer below reads as "no bound" -- Go's zero time here, and an
// omitted field on the wire -- so a bound that resolves to it would be dropped
// from the request without a word, which is the silence this check exists to
// break rather than to join.
func traceBound(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, messages.TraceWindowNotATime(field, value)
	}
	if parsed.Unix() <= 0 {
		return time.Time{}, messages.TraceWindowBoundUnusable(field, value)
	}
	return parsed, nil
}
