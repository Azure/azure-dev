// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"azure.ai.latency/internal/model"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func resolveTimeWindow(
	last string,
	startTime string,
	endTime string,
	lastExplicit bool,
	now time.Time,
) (model.TimeWindow, error) {
	hasStart := strings.TrimSpace(startTime) != ""
	hasEnd := strings.TrimSpace(endTime) != ""
	if hasStart != hasEnd {
		return model.TimeWindow{}, validationError(
			"incomplete_time_range",
			"--start-time and --end-time must be supplied together.",
			"Provide both RFC3339 timestamps, or use --last.",
		)
	}
	if hasStart && lastExplicit {
		return model.TimeWindow{}, validationError(
			"conflicting_time_range",
			"--last cannot be combined with --start-time and --end-time.",
			"Choose either a relative or exact time range.",
		)
	}
	if hasStart {
		start, err := time.Parse(time.RFC3339, startTime)
		if err != nil {
			return model.TimeWindow{}, validationError(
				"invalid_start_time",
				fmt.Sprintf("Invalid --start-time %q.", startTime),
				"Use an RFC3339 timestamp such as 2026-09-01T08:00:00Z.",
			)
		}
		end, err := time.Parse(time.RFC3339, endTime)
		if err != nil {
			return model.TimeWindow{}, validationError(
				"invalid_end_time",
				fmt.Sprintf("Invalid --end-time %q.", endTime),
				"Use an RFC3339 timestamp such as 2026-09-02T08:00:00Z.",
			)
		}
		if !end.After(start) {
			return model.TimeWindow{}, validationError(
				"invalid_time_range",
				"--end-time must be later than --start-time.",
				"Choose a positive UTC time range.",
			)
		}
		return model.TimeWindow{
			StartTime: start.UTC().Format(time.RFC3339),
			EndTime:   end.UTC().Format(time.RFC3339),
			Label:     exactRangeLabel(start.UTC(), end.UTC()),
		}, nil
	}

	if strings.TrimSpace(last) == "" {
		last = "24h"
	}
	duration, err := parseRelativeDuration(last)
	if err != nil || duration <= 0 {
		return model.TimeWindow{}, validationError(
			"invalid_last_duration",
			fmt.Sprintf("Invalid --last duration %q.", last),
			"Use a positive duration such as 30m, 24h, or 7d.",
		)
	}
	end := now.UTC().Truncate(time.Second)
	start := end.Add(-duration)
	return model.TimeWindow{
		StartTime: start.Format(time.RFC3339),
		EndTime:   end.Format(time.RFC3339),
		Label:     relativeRangeLabel(duration),
	}, nil
}

func parseRelativeDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if before, ok := strings.CutSuffix(value, "d"); ok {
		days, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(value)
}

func relativeRangeLabel(duration time.Duration) string {
	switch {
	case duration >= 48*time.Hour && duration%(24*time.Hour) == 0:
		days := int(duration / (24 * time.Hour))
		return fmt.Sprintf("Last %d %s", days, plural(days, "day", "days"))
	case duration%time.Hour == 0:
		hours := int(duration / time.Hour)
		return fmt.Sprintf("Last %d %s", hours, plural(hours, "hour", "hours"))
	default:
		minutes := int(duration / time.Minute)
		return fmt.Sprintf("Last %d %s", minutes, plural(minutes, "minute", "minutes"))
	}
}

func exactRangeLabel(start, end time.Time) string {
	return fmt.Sprintf("%s to %s", start.Format(time.RFC3339), end.Format(time.RFC3339))
}

func plural(value int, singular, pluralValue string) string {
	if value == 1 {
		return singular
	}
	return pluralValue
}

func validationError(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}
