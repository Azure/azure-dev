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

// TraceAgentName says whose conversations a trace eval reads.
//
// `agent_name` is the filter; an eval that leaves it off and names a target
// still means "this agent's traces". A model target is not an agent: it names a
// deployment, and filtering spans by a deployment name matches nothing, so the
// run comes back empty with no reason given. `target.type` is optional, and an
// untyped target is read as an agent, which is how the dataset path reads one.
//
// One definition, because the configuration check and the request builder both
// have to answer this and used to answer it separately: the config accepted a
// model target and every run of it then failed.
func TraceAgentName(source *SourceDecl, target *Target) string {
	if source != nil && source.AgentName != "" {
		return source.AgentName
	}
	if target == nil || target.Type == TargetTypeModel {
		return ""
	}
	return target.Name
}

// ValidateSource checks a source declaration and resolves the window it names.
//
// One definition of what a source may say, called by the configuration check
// and again when the request is built. Two copies drifted apart on every axis
// they were not both tested on, so which rules applied depended on which door
// the eval came through.
//
// The window is returned as well as checked, because a rule about a window can
// only be stated once the window is known, and the caller that sends the
// request needs the same bounds the caller that validated it saw.
//
// A zero start or end means unbounded at that end.
func ValidateSource(source *SourceDecl) (start, end time.Time, err error) {
	if source == nil {
		return time.Time{}, time.Time{}, nil
	}
	if err := validateSourceFields(source); err != nil {
		return time.Time{}, time.Time{}, err
	}
	return resolveTraceWindow(source)
}

// validateSourceFields refuses fields the declared source type does not read.
//
// A field that is quietly ignored is how a file comes to say something it does
// not do: a `lookback_hours` under `type: responses` looks like it bounds the
// run and never has, and nothing about the run it produces says otherwise.
func validateSourceFields(source *SourceDecl) error {
	var inert []string
	switch source.Type {
	case SourceTypeTraces:
		inert = namesOfSet(
			sourceField{"response_ids", len(source.ResponseIDs) > 0},
			sourceField{"max_turns", source.MaxTurns != 0},
		)
	case SourceTypeResponses:
		if source.MaxTurns < 0 {
			return messages.MaxTurnsUnusable(source.MaxTurns)
		}
		inert = namesOfSet(
			sourceField{"start_time", source.StartTime != ""},
			sourceField{"end_time", source.EndTime != ""},
			sourceField{"lookback_hours", source.LookbackHours != 0},
			sourceField{"max_traces", source.MaxTraces != 0},
			sourceField{"agent_name", source.AgentName != ""},
			sourceField{"agent_version", source.AgentVersion != ""},
		)
	default:
		// An unsupported type is reported by the caller, which knows how to
		// name the eval it came from and which types there are.
		return nil
	}
	if len(inert) == 0 {
		return nil
	}
	return messages.SourceFieldsNotRead(source.Type, inert)
}

type sourceField struct {
	name string
	set  bool
}

func namesOfSet(fields ...sourceField) []string {
	var names []string
	for _, f := range fields {
		if f.set {
			names = append(names, f.name)
		}
	}
	return names
}

// resolveTraceWindow reads the span of traces an eval grades.
//
// The sole enforcement point for the sign and size of a lookback, the sign of
// max_traces, a window declared twice over, and the pre-epoch rule for both a
// written bound and a derived one. A new rule about the window belongs here.
func resolveTraceWindow(source *SourceDecl) (start, end time.Time, err error) {
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
		start = from.Add(-time.Duration(source.LookbackHours) * time.Hour)
		// Held to the same rule as a written bound. A lookback long enough to
		// reach past the epoch lands on a start the wire then drops, which is
		// the silence the rule exists to break however the bound was reached.
		if start.Unix() <= 0 {
			return time.Time{}, time.Time{}, messages.LookbackReachesTooFarBack(source.LookbackHours)
		}
		return start, end, nil
	}

	if !start.IsZero() && !end.IsZero() && !end.After(start) {
		return time.Time{}, time.Time{}, messages.TraceWindowEndsBeforeItStarts(
			source.StartTime, source.EndTime)
	}
	return start, end, nil
}

// traceBound reads one end of the window.
//
// A bound at or before the Unix epoch is refused. Exactly zero has to be
// refused because zero is what every layer below reads as "no bound" -- Go's
// zero time here, and an omitted field on the wire -- so a bound that lands on
// it would be dropped from the request without a word. The rest of the
// pre-1970 half-line is refused with it because no trace was recorded there,
// and one rule about the whole span is easier to state than a hole in it.
func traceBound(name, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, messages.TraceWindowNotATime(name, value)
	}
	if parsed.Unix() <= 0 {
		return time.Time{}, messages.TraceWindowBoundUnusable(name, value)
	}
	return parsed, nil
}
