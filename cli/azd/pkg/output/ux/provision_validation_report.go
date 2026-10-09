// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// ProvisionValidationReportItem represents a single finding from provision validation.
type ProvisionValidationReportItem struct {
	// IsError is true for blocking errors, false for warnings.
	IsError bool
	// IsCritical marks a warning that defaults confirmation to No.
	// It is ignored for blocking errors.
	IsCritical bool
	// DiagnosticID is a unique, stable identifier for this finding type (e.g.
	// "role_assignment_missing"). Used in telemetry for error correlation.
	DiagnosticID string
	// Message describes the finding.
	Message string
	// Suggestion is an optional actionable recommendation for resolving the issue.
	Suggestion string
	// Links is an optional list of reference links related to the finding.
	Links []ProvisionValidationReportLink
}

// ProvisionValidationReportLink represents a reference link attached to a validation report item.
type ProvisionValidationReportLink struct {
	// URL is the link target.
	URL string
	// Title is the display text for terminal hyperlinks (optional).
	// In non-terminal output the URL is shown regardless of Title.
	Title string
}

// ProvisionValidationReport displays the results of local provision validation.
// Warnings are shown first, with indented details and bold critical warning headings.
// Warning-only reports end with a warning total; blocking errors follow warnings without a total.
type ProvisionValidationReport struct {
	Items []ProvisionValidationReportItem
}

func (r *ProvisionValidationReport) ToString(currentIndentation string) string {
	warnings, errors := r.partition()
	if len(warnings) == 0 && len(errors) == 0 {
		return ""
	}

	var sb strings.Builder

	for i, w := range warnings {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		writeItem(&sb, currentIndentation, warningPrefix, w)
	}

	if len(warnings) > 0 && len(errors) > 0 {
		sb.WriteString("\n\n")
	}

	for i, e := range errors {
		if i > 0 {
			sb.WriteString("\n")
		}
		writeItem(&sb, currentIndentation, failedPrefix, e)
	}

	if len(warnings) > 0 && len(errors) == 0 {
		noun := "warnings"
		if len(warnings) == 1 {
			noun = "warning"
		}
		summary := fmt.Sprintf("%d %s found", len(warnings), noun)
		if criticalCount := r.CriticalWarningCount(); criticalCount > 0 {
			summary += fmt.Sprintf(" (%d critical)", criticalCount)
		}
		sb.WriteString(fmt.Sprintf("\n\n%s%s",
			currentIndentation, output.WithWarningFormat("%s.", summary)))
	}

	return sb.String()
}

// writeItem renders a single report item with multi-line support.
// Warning details are indented four spaces beyond the heading, with a blank line before suggestions.
// Critical warnings have a bold yellow heading; blocking errors retain their existing layout.
func writeItem(
	sb *strings.Builder, indent string, prefix string, item ProvisionValidationReportItem,
) {
	if item.Message == "" {
		return
	}
	lines := strings.Split(item.Message, "\n")
	detailIndent := indent
	if !item.IsError {
		detailIndent += "    "
	}

	if !item.IsError && item.IsCritical {
		sb.WriteString(indent)
		sb.WriteString(output.WithBold("%s",
			output.WithWarningFormat("(!) Critical warning: %s", lines[0])))
	} else {
		sb.WriteString(fmt.Sprintf("%s%s %s", indent, prefix, lines[0]))
	}

	for _, line := range lines[1:] {
		sb.WriteString("\n")
		if line != "" {
			sb.WriteString(detailIndent)
			sb.WriteString(line)
		}
	}

	if item.Suggestion != "" {
		suggestion := item.Suggestion
		if !item.IsError {
			sb.WriteString("\n")
			suggestion = strings.ReplaceAll(suggestion, "\n", "\n"+detailIndent)
		}
		sb.WriteString(fmt.Sprintf("\n%s%s %s",
			detailIndent,
			output.WithHighLightFormat("Suggestion:"),
			suggestion))
	}
	for _, link := range item.Links {
		if link.Title != "" {
			sb.WriteString(fmt.Sprintf("\n%s• %s",
				detailIndent,
				output.WithHyperlink(link.URL, link.Title)))
		} else {
			sb.WriteString(fmt.Sprintf("\n%s• %s",
				detailIndent,
				output.WithLinkFormat(link.URL)))
		}
	}
}

func (r *ProvisionValidationReport) MarshalJSON() ([]byte, error) {
	warnings, errors := r.partition()

	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("provision validation: %d warning(s), %d error(s)",
			len(warnings), len(errors))))
}

// HasErrors returns true if the report contains at least one error-level item.
func (r *ProvisionValidationReport) HasErrors() bool {
	for _, item := range r.Items {
		if item.IsError {
			return true
		}
	}
	return false
}

// HasWarnings returns true if the report contains at least one warning-level item.
func (r *ProvisionValidationReport) HasWarnings() bool {
	for _, item := range r.Items {
		if !item.IsError {
			return true
		}
	}
	return false
}

// CriticalWarningCount returns the number of critical warning-level items.
func (r *ProvisionValidationReport) CriticalWarningCount() int {
	count := 0
	for _, item := range r.Items {
		if !item.IsError && item.IsCritical {
			count++
		}
	}
	return count
}

// partition splits items into warnings and errors, preserving order within each group.
func (r *ProvisionValidationReport) partition() (warnings, errors []ProvisionValidationReportItem) {
	for _, item := range r.Items {
		if item.IsError {
			errors = append(errors, item)
		} else {
			warnings = append(warnings, item)
		}
	}
	return warnings, errors
}
