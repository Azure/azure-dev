// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"
	"time"
)

func TestResolveTimeWindowRelative(t *testing.T) {
	now := time.Date(2026, time.September, 11, 4, 0, 0, 0, time.UTC)
	window, err := resolveTimeWindow("24h", "", "", false, now)
	if err != nil {
		t.Fatal(err)
	}
	if window.StartTime != "2026-09-10T04:00:00Z" ||
		window.EndTime != "2026-09-11T04:00:00Z" ||
		window.Label != "Last 24 hours" {
		t.Fatalf("unexpected window: %+v", window)
	}
}

func TestResolveTimeWindowDays(t *testing.T) {
	now := time.Date(2026, time.September, 11, 4, 0, 0, 0, time.UTC)
	window, err := resolveTimeWindow("7d", "", "", true, now)
	if err != nil {
		t.Fatal(err)
	}
	if window.Label != "Last 7 days" {
		t.Fatalf("label = %q, want Last 7 days", window.Label)
	}
}

func TestResolveTimeWindowRejectsIncompleteExactRange(t *testing.T) {
	_, err := resolveTimeWindow("", "2026-09-01T08:00:00Z", "", false, time.Now())
	if err == nil {
		t.Fatal("expected incomplete exact range to fail")
	}
}

func TestResolveTimeWindowRejectsConflictingInputs(t *testing.T) {
	_, err := resolveTimeWindow(
		"24h",
		"2026-09-01T08:00:00Z",
		"2026-09-02T08:00:00Z",
		true,
		time.Now(),
	)
	if err == nil {
		t.Fatal("expected conflicting relative and exact ranges to fail")
	}
}
