// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/mattn/go-colorable"
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
	// Title is the display text (optional — if empty, the URL is shown).
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

	type jsonLink struct {
		URL   string `json:"url"`
		Title string `json:"title,omitempty"`
	}
	type jsonItem struct {
		Severity     string     `json:"severity"`
		DiagnosticID string     `json:"diagnosticId,omitempty"`
		Message      string     `json:"message"`
		Suggestion   string     `json:"suggestion,omitempty"`
		Links        []jsonLink `json:"links,omitempty"`
	}

	// Use partition ordering (warnings first, then errors)
	// to match ToString() output order.
	ordered := make(
		[]PreflightReportItem, 0, len(warnings)+len(errors))
	ordered = append(ordered, warnings...)
	ordered = append(ordered, errors...)

	items := make([]jsonItem, 0, len(ordered))
	for _, item := range ordered {
		severity := "warning"
		if item.IsError {
			severity = "error"
		}
		ji := jsonItem{
			Severity:     severity,
			DiagnosticID: item.DiagnosticID,
			Message:      stripAnsi(item.Message),
			Suggestion:   stripAnsi(item.Suggestion),
		}
		for _, link := range item.Links {
			ji.Links = append(ji.Links, jsonLink(link))
		}
		items = append(items, ji)
	}

	result := struct {
		Type    string     `json:"type"`
		Summary string     `json:"summary"`
		Items   []jsonItem `json:"items"`
	}{
		Type: "preflight",
		Summary: fmt.Sprintf("preflight: %d warning(s), %d error(s)",
			len(warnings), len(errors)),
		Items: items,
	}

	return json.Marshal(result)
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

// stripAnsi removes ANSI escape sequences from a string for
// machine-readable output (e.g. JSON).
func stripAnsi(s string) string {
	if s == "" {
		return s
	}
	var buf bytes.Buffer
	// colorable.NewNonColorable strips ANSI sequences.
	if _, err := io.Copy(
		colorable.NewNonColorable(&buf),
		strings.NewReader(s),
	); err != nil {
		return s
	}
	return buf.String()
}
