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
	// Message describes the finding.
	Message string
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
		sb.WriteString(fmt.Sprintf("%s%s %s", currentIndentation, warningPrefix, w.Message))
	}

	if len(warnings) > 0 && len(errors) > 0 {
		sb.WriteString("\n")
	}

	for i, e := range errors {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("%s%s %s", currentIndentation, failedPrefix, e.Message))
	}

	return sb.String()
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
