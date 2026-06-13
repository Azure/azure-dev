// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stringPtr is a small local helper for tests that build pointer
// fields of the Azure SDK structs. Local rather than exported so we
// don't leak a generic "make a pointer" helper into production code.
//
//go:fix inline
func stringPtr(s string) *string { return new(s) }

// changeTypePtr is the equivalent for armresources.ChangeType so the
// table-driven tests stay terse.
//
//go:fix inline
func changeTypePtr(c armresources.ChangeType) *armresources.ChangeType { return new(c) }

func TestSummarizeWhatIf_NilSafe(t *testing.T) {
	t.Parallel()

	// Empty result: status defaults, no rows, no panic.
	got := summarizeWhatIf(armresources.WhatIfOperationResult{})
	assert.Contains(t, got, "What-if status: Succeeded",
		"default status should be Succeeded when Status is nil")
	assert.Contains(t, got, "Total changes: 0")
	assert.NotContains(t, got, "Affected resources",
		"empty change list omits the affected-resources block")

	// Status set, no Properties: no panic on nil Properties.
	status := "Succeeded"
	got = summarizeWhatIf(armresources.WhatIfOperationResult{Status: &status})
	assert.Contains(t, got, "What-if status: Succeeded")
	assert.Contains(t, got, "Total changes: 0")

	// Properties set with all-nil Changes entries: silently skipped.
	props := &armresources.WhatIfOperationProperties{
		Changes: []*armresources.WhatIfChange{nil, nil, nil},
	}
	got = summarizeWhatIf(armresources.WhatIfOperationResult{Properties: props})
	assert.Contains(t, got, "Total changes: 0",
		"nil changes must NOT be counted")

	// Empty status string: still defaults to Succeeded.
	empty := ""
	got = summarizeWhatIf(armresources.WhatIfOperationResult{Status: &empty})
	assert.Contains(t, got, "What-if status: Succeeded",
		"empty string Status should be treated as missing -> default")
}

func TestSummarizeWhatIf_DetailedBreakdown(t *testing.T) {
	t.Parallel()

	id1 := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-test" +
		"/providers/Microsoft.CognitiveServices/accounts/cog-abc"
	id2 := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-test" +
		"/providers/Microsoft.CognitiveServices/accounts/cog-abc/projects/my-project"
	id3 := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-test" +
		"/providers/Microsoft.ContainerRegistry/registries/cr-abc"

	props := &armresources.WhatIfOperationProperties{
		Changes: []*armresources.WhatIfChange{
			{ChangeType: new(armresources.ChangeTypeCreate), ResourceID: &id1},
			{ChangeType: new(armresources.ChangeTypeCreate), ResourceID: &id2},
			{ChangeType: new(armresources.ChangeTypeModify), ResourceID: &id3},
			{ChangeType: new(armresources.ChangeTypeDelete), ResourceID: new("/subs/.../old")},
		},
	}
	got := summarizeWhatIf(armresources.WhatIfOperationResult{Properties: props})

	assert.Contains(t, got, "Total changes: 4")
	assert.Contains(t, got, "Create: 2")
	assert.Contains(t, got, "Modify: 1")
	assert.Contains(t, got, "Delete: 1")
	assert.NotContains(t, got, "Deploy: ",
		"zero-count rows are omitted from the per-type block")
	assert.NotContains(t, got, "NoChange: ")

	// Resource IDs are shortened past `/providers/` so the diff
	// stays readable. The literal id1 prefix should NOT appear.
	assert.NotContains(t, got, "/subscriptions/00000000-0000-0000-0000-000000000001")
	assert.Contains(t, got, "Microsoft.CognitiveServices/accounts/cog-abc")
	assert.Contains(t, got, "+ Create Microsoft.CognitiveServices/accounts/cog-abc",
		"resource lines use the + <change-type> <short-id> shape")

	// Deterministic per-type order: Create lines appear before Modify
	// lines in the per-type block.
	createIdx := strings.Index(got, "Create: 2")
	modifyIdx := strings.Index(got, "Modify: 1")
	require.Positive(t, createIdx, "Create row must be present")
	require.Greater(t, modifyIdx, createIdx,
		"per-type block must list Create before Modify (orderedChangeTypes)")
}

func TestSummarizeWhatIf_TruncatesLongList(t *testing.T) {
	t.Parallel()

	// Build 25 Create rows; exceeds maxPreviewResources (20).
	const total = 25
	createT := armresources.ChangeTypeCreate
	changes := make([]*armresources.WhatIfChange, 0, total)
	for i := range total {
		id := "/providers/Microsoft.Foo/bar/" + string(rune('a'+i%26))
		changes = append(changes, &armresources.WhatIfChange{
			ChangeType: &createT,
			ResourceID: &id,
		})
	}
	got := summarizeWhatIf(armresources.WhatIfOperationResult{
		Properties: &armresources.WhatIfOperationProperties{Changes: changes},
	})

	assert.Contains(t, got, "Total changes: 25",
		"count line reflects the TRUE total even when the list is truncated")
	assert.Contains(t, got, "... and 5 more",
		"footer notes the truncated remainder")
	// Count the "  + " lines: should be exactly maxPreviewResources.
	resourceLines := strings.Count(got, "\n  + ")
	assert.Equal(t, maxPreviewResources, resourceLines)
}

func TestSummarizeWhatIf_UnknownChangeTypePassesThrough(t *testing.T) {
	t.Parallel()

	// A change with no ChangeType (nil) must fall back to "Unknown"
	// rather than panicking or producing an empty per-type row.
	id := "/providers/Microsoft.Foo/bar/x"
	props := &armresources.WhatIfOperationProperties{
		Changes: []*armresources.WhatIfChange{
			{ResourceID: &id}, // ChangeType nil
		},
	}
	got := summarizeWhatIf(armresources.WhatIfOperationResult{Properties: props})
	assert.Contains(t, got, "Unknown: 1")
	assert.Contains(t, got, "+ Unknown Microsoft.Foo/bar/x")
}

func TestSummarizeWhatIf_MissingResourceIDIsPlaceheld(t *testing.T) {
	t.Parallel()

	// ResourceID nil -> show placeholder rather than crashing.
	props := &armresources.WhatIfOperationProperties{
		Changes: []*armresources.WhatIfChange{
			{ChangeType: new(armresources.ChangeTypeCreate)},
		},
	}
	got := summarizeWhatIf(armresources.WhatIfOperationResult{Properties: props})
	assert.Contains(t, got, "<unknown resource>")
}

func TestShortenResourceID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "trims /subscriptions/.../resourceGroups/.../providers/ prefix",
			in: "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/foo",
			want: "Microsoft.CognitiveServices/accounts/foo",
		},
		{
			name: "no /providers/ marker -> passthrough",
			in:   "no_marker_here",
			want: "no_marker_here",
		},
		{
			name: "empty -> empty",
			in:   "",
			want: "",
		},
		{
			name: "marker at end -> empty suffix",
			in:   "/subs/x/providers/",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, shortenResourceID(c.in))
		})
	}
}

func TestWhatIfFailure_Success(t *testing.T) {
	t.Parallel()

	// Nil result -> nil error. Lets callers treat "no failure
	// detected" as the happy path without juggling sentinels.
	assert.NoError(t, whatIfFailure(armresources.WhatIfOperationResult{}))

	// Status: Succeeded (any casing) -> nil error.
	for _, s := range []string{"Succeeded", "succeeded", "SUCCEEDED"} {
		err := whatIfFailure(armresources.WhatIfOperationResult{Status: &s})
		assert.NoError(t, err, "Status=%q must be treated as success", s)
	}
}

func TestWhatIfFailure_InlineErrorIsSurfaced(t *testing.T) {
	t.Parallel()

	// ARM returns HTTP 200 with Properties.Error populated for
	// preflight failures. whatIfFailure must surface this as
	// CodeArmWhatIfFailed so the caller doesn't silently report
	// "0 changes".
	inner := &armresources.ErrorResponse{
		Code:    new("InsufficientQuota"),
		Message: new("This operation requires 10 capacity..."),
	}
	outer := &armresources.ErrorResponse{
		Code:    new("InvalidTemplateDeployment"),
		Message: new("Preflight validation failed."),
		Details: []*armresources.ErrorResponse{inner},
	}
	err := whatIfFailure(armresources.WhatIfOperationResult{Error: outer})
	require.Error(t, err)

	var local *azdext.LocalError
	require.True(t, errors.As(err, &local), "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeArmWhatIfFailed, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category,
		"inline ARM what-if failures are user-fixable -> Validation")
	// Both the outer wrapper and the inner cause must be visible so
	// the user sees the actual reason (the inner one).
	assert.Contains(t, local.Message, "InvalidTemplateDeployment")
	assert.Contains(t, local.Message, "InsufficientQuota")
	assert.NotEmpty(t, local.Suggestion)
}

func TestWhatIfFailure_NonSucceededStatus(t *testing.T) {
	t.Parallel()

	failed := "Failed"
	err := whatIfFailure(armresources.WhatIfOperationResult{Status: &failed})
	require.Error(t, err)
	var local *azdext.LocalError
	require.True(t, errors.As(err, &local))
	assert.Equal(t, exterrors.CodeArmWhatIfFailed, local.Code)
	assert.Contains(t, local.Message, "Failed")
}

func TestFormatArmErrorResponse(t *testing.T) {
	t.Parallel()

	t.Run("nil -> placeholder string", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "(no error detail)", formatArmErrorResponse(nil))
	})

	t.Run("flat error: code + message", func(t *testing.T) {
		t.Parallel()
		got := formatArmErrorResponse(&armresources.ErrorResponse{
			Code:    new("InvalidTemplate"),
			Message: new("Template syntax error at line 5"),
		})
		assert.Equal(t, "InvalidTemplate: Template syntax error at line 5", got)
	})

	t.Run("nested details flatten recursively", func(t *testing.T) {
		t.Parallel()
		got := formatArmErrorResponse(&armresources.ErrorResponse{
			Code:    new("OuterCode"),
			Message: new("outer msg"),
			Details: []*armresources.ErrorResponse{
				nil, // nil entries silently skipped
				{
					Code:    new("InnerCode"),
					Message: new("inner msg"),
				},
			},
		})
		assert.Contains(t, got, "OuterCode: outer msg")
		assert.Contains(t, got, "InnerCode: inner msg")
		assert.Contains(t, got, ";", "details are joined with `; `")
	})

	t.Run("empty fields collapse to placeholder", func(t *testing.T) {
		t.Parallel()
		// Both code and message empty / nil: don't return ": "
		// (would look like a parse bug to the user).
		assert.Equal(t, "(empty ARM error)",
			formatArmErrorResponse(&armresources.ErrorResponse{}))
	})

	t.Run("code only is allowed", func(t *testing.T) {
		t.Parallel()
		got := formatArmErrorResponse(&armresources.ErrorResponse{
			Code: new("SomeCode"),
		})
		assert.Equal(t, "SomeCode:", got)
	})
}
