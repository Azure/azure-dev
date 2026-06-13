// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// maxPreviewResources caps how many "Affected resources" lines
// summarizeWhatIf renders. The counts still reflect the full set; only the
// per-resource enumeration is truncated.
const maxPreviewResources = 20

// orderedChangeTypes is the stable display order for the per-type count block,
// keeping the summary deterministic for tests.
var orderedChangeTypes = []string{
	"Create",
	"Modify",
	"Delete",
	"Deploy",
	"NoChange",
	"Ignore",
	"Unknown",
}

// summarizeWhatIf renders a deterministic, multi-line summary of a successful
// what-if result. Nil-safe on Properties, Status, and individual Change
// entries. Format (test snapshots anchor on this layout):
//
//	What-if status: <Status>
//	Total changes: <N>
//	  Create: <n>
//	  Modify: <n>
//	  Delete: <n>
//	  Deploy: <n>
//	  NoChange: <n>
//	  Ignore: <n>
//	Affected resources:
//	  + <change-type> <resource-id>
//	  ... and <K> more
//
// Status defaults to "Succeeded" when ARM returned empty. Zero-count rows are
// omitted; the resource list is truncated at maxPreviewResources.
func summarizeWhatIf(r armresources.WhatIfOperationResult) string {
	status := "Succeeded"
	if r.Status != nil && *r.Status != "" {
		status = *r.Status
	}

	var changes []*armresources.WhatIfChange
	if r.Properties != nil {
		changes = r.Properties.Changes
	}

	counts := map[string]int{}
	type row struct{ changeType, resourceID string }
	rows := make([]row, 0, len(changes))
	for _, c := range changes {
		if c == nil {
			continue
		}
		ct := "Unknown"
		if c.ChangeType != nil {
			ct = string(*c.ChangeType)
		}
		counts[ct]++

		rid := "<unknown resource>"
		if c.ResourceID != nil {
			rid = *c.ResourceID
		}
		rows = append(rows, row{changeType: ct, resourceID: rid})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "What-if status: %s\n", status)
	fmt.Fprintf(&b, "Total changes: %d", len(rows))
	for _, t := range orderedChangeTypes {
		if n := counts[t]; n > 0 {
			fmt.Fprintf(&b, "\n  %s: %d", t, n)
		}
	}

	if len(rows) > 0 {
		b.WriteString("\nAffected resources:")
		shown := rows
		truncated := false
		if len(shown) > maxPreviewResources {
			shown = shown[:maxPreviewResources]
			truncated = true
		}
		for _, r := range shown {
			fmt.Fprintf(&b, "\n  + %s %s", r.changeType, shortenResourceID(r.resourceID))
		}
		if truncated {
			fmt.Fprintf(&b, "\n  ... and %d more", len(rows)-maxPreviewResources)
		}
	}

	return b.String()
}

// shortenResourceID trims the subscription/resource-group prefix from an ARM
// resource id so previews stay readable. Falls back to the original id when it
// doesn't match the expected shape.
func shortenResourceID(id string) string {
	const marker = "/providers/"
	if _, after, ok := strings.Cut(id, marker); ok {
		return after
	}
	return id
}

// whatIfFailure returns a structured error when ARM reports failure inline
// (HTTP 200 with Error set, or Status != "Succeeded"). Returns nil on success.
// The inline-error path catches preflight failures (quota, template
// validation) that would otherwise look like "0 changes".
func whatIfFailure(r armresources.WhatIfOperationResult) error {
	if r.Error != nil {
		return exterrors.Validation(
			exterrors.CodeArmWhatIfFailed,
			"ARM what-if reported a failure: "+formatArmErrorResponse(r.Error),
			"fix the underlying ARM error and retry `azd provision --preview`",
		)
	}
	if r.Status != nil && !strings.EqualFold(*r.Status, "Succeeded") {
		return exterrors.Validation(
			exterrors.CodeArmWhatIfFailed,
			fmt.Sprintf("ARM what-if status: %s", *r.Status),
			"fix the underlying ARM error and retry `azd provision --preview`",
		)
	}
	return nil
}

// formatArmErrorResponse flattens an ARM ErrorResponse into a single line,
// walking Details recursively so the user sees the real nested cause (e.g.
// "InsufficientQuota") rather than just the generic outer wrapper.
func formatArmErrorResponse(e *armresources.ErrorResponse) string {
	if e == nil {
		return "(no error detail)"
	}
	code, msg := "", ""
	if e.Code != nil {
		code = *e.Code
	}
	if e.Message != nil {
		msg = *e.Message
	}
	var out strings.Builder
	out.WriteString(strings.TrimSpace(code + ": " + msg))
	for _, d := range e.Details {
		if d == nil {
			continue
		}
		out.WriteString("; " + formatArmErrorResponse(d))
	}
	if out.String() == ":" || out.String() == "" {
		return "(empty ARM error)"
	}
	return out.String()
}
