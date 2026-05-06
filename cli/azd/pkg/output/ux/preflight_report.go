// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// PreflightReportItem represents a single finding from preflight validation.
type PreflightReportItem struct {
	// IsError is true for blocking errors, false for warnings.
	IsError bool
	// DiagnosticID is a unique, stable identifier for this finding type (e.g.
	// "role_assignment_missing"). Used in telemetry for error correlation.
	DiagnosticID string
	// Message describes the finding.
	Message string
	// Suggestion is an optional actionable recommendation for resolving the issue.
	Suggestion string
	// Links is an optional list of reference links related to the finding.
	Links []PreflightReportLink
}

// PreflightReportLink represents a reference link attached to a preflight report item.
type PreflightReportLink struct {
	// URL is the link target.
	URL string
	// Title is the display text for terminal hyperlinks (optional).
	// In non-terminal output the URL is shown regardless of Title.
	Title string
}

// PreflightReport displays the results of local preflight validation.
// Warnings are shown first, followed by errors. Each entry is separated by a blank line.
type PreflightReport struct {
	Items []PreflightReportItem
}

func (r *PreflightReport) ToString(currentIndentation string) string {
	warnings, errors := r.partition()
	if len(warnings) == 0 && len(errors) == 0 {
		return ""
	}

	var sb strings.Builder

	for i, w := range warnings {
		if i > 0 {
			sb.WriteString("\n")
		}
		writeItem(&sb, currentIndentation, warningPrefix, w)
	}

	if len(warnings) > 0 && len(errors) > 0 {
		sb.WriteString("\n")
	}

	for i, e := range errors {
		if i > 0 {
			sb.WriteString("\n")
		}
		writeItem(&sb, currentIndentation, failedPrefix, e)
	}

	return sb.String()
}

// writeItem renders a single report item with multi-line support.
// The first line is prefixed with the status indicator (e.g. "(!) Warning:").
// Continuation lines in the message are indented at the same level as the prefix.
func writeItem(
	sb *strings.Builder, indent string, prefix string, item PreflightReportItem,
) {
	if item.Message == "" {
		return
	}
	lines := strings.Split(item.Message, "\n")
	sb.WriteString(fmt.Sprintf("%s%s %s", indent, prefix, lines[0]))
	for _, line := range lines[1:] {
		sb.WriteString(fmt.Sprintf("\n%s%s", indent, line))
	}

	if item.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("\n%s%s %s",
			indent,
			output.WithHighLightFormat("Suggestion:"),
			item.Suggestion))
	}
	for _, link := range item.Links {
		if link.Title != "" {
			sb.WriteString(fmt.Sprintf("\n%s• %s",
				indent,
				output.WithHyperlink(link.URL, link.Title)))
		} else {
			sb.WriteString(fmt.Sprintf("\n%s• %s",
				indent,
				output.WithLinkFormat(link.URL)))
		}
	}
}

func (r *PreflightReport) MarshalJSON() ([]byte, error) {
	warnings, errors := r.partition()

	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("preflight: %d warning(s), %d error(s)",
			len(warnings), len(errors))))
}

// HasErrors returns true if the report contains at least one error-level item.
func (r *PreflightReport) HasErrors() bool {
	for _, item := range r.Items {
		if item.IsError {
			return true
		}
	}
	return false
}

// HasWarnings returns true if the report contains at least one warning-level item.
func (r *PreflightReport) HasWarnings() bool {
	for _, item := range r.Items {
		if !item.IsError {
			return true
		}
	}
	return false
}

// partition splits items into warnings and errors, preserving order within each group.
func (r *PreflightReport) partition() (warnings, errors []PreflightReportItem) {
	for _, item := range r.Items {
		if item.IsError {
			errors = append(errors, item)
		} else {
			warnings = append(warnings, item)
		}
	}
	return warnings, errors
}
