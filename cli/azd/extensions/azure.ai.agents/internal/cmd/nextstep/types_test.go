// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import "testing"

func TestBuildJSON_Empty(t *testing.T) {
	if got := BuildJSON(nil, ""); got != nil {
		t.Fatalf("expected nil for empty suggestions, got %+v", got)
	}
	if got := BuildJSON([]Suggestion{}, "tip"); got != nil {
		t.Fatalf("expected nil for empty slice even with note, got %+v", got)
	}
}

func TestBuildJSON_Primary(t *testing.T) {
	got := BuildJSON([]Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally"},
	}, "")
	if got == nil || got.Primary == nil {
		t.Fatalf("expected primary, got %+v", got)
	}
	if got.Primary.Command != "azd ai agent run" || got.Primary.Description != "start the agent locally" {
		t.Fatalf("primary mismatch: %+v", got.Primary)
	}
	if got.Secondary != nil {
		t.Fatalf("expected nil secondary, got %+v", got.Secondary)
	}
	if got.Note != "" {
		t.Fatalf("expected empty note, got %q", got.Note)
	}
}

func TestBuildJSON_PrimarySecondaryAndNote(t *testing.T) {
	got := BuildJSON([]Suggestion{
		{Command: "azd ai agent run", Description: "start the agent locally"},
		{Command: "azd ai agent invoke --local 'Hi!'", Description: "test it in another terminal"},
		{Command: "azd ai agent show", Description: "third entry should be ignored"},
	}, "Tip: see README.md for sample payloads.")
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Primary == nil || got.Primary.Command != "azd ai agent run" {
		t.Fatalf("primary mismatch: %+v", got.Primary)
	}
	if got.Secondary == nil || got.Secondary.Command != "azd ai agent invoke --local 'Hi!'" {
		t.Fatalf("secondary mismatch: %+v", got.Secondary)
	}
	if got.Note != "Tip: see README.md for sample payloads." {
		t.Fatalf("note mismatch: %q", got.Note)
	}
}
