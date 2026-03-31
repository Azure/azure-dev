// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"fmt"
	"strings"
)

// FindingSeverity indicates whether a finding is an error or a warning.
type FindingSeverity string

const (
	SeverityError   FindingSeverity = "Error"
	SeverityWarning FindingSeverity = "Warning"
)

// ValidationFinding represents a single validation issue found in a job definition.
type ValidationFinding struct {
	Field    string
	Severity FindingSeverity
	Message  string
}

// ValidationResult holds the overall result of job validation.
type ValidationResult struct {
	Findings []ValidationFinding
}

// HasErrors returns true if any finding is an error.
func (r *ValidationResult) HasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// ErrorCount returns the number of error findings.
func (r *ValidationResult) ErrorCount() int {
	count := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			count++
		}
	}
	return count
}

// WarningCount returns the number of warning findings.
func (r *ValidationResult) WarningCount() int {
	count := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityWarning {
			count++
		}
	}
	return count
}

// ValidateJobOffline performs offline validation of a job definition.
// It returns all findings (errors and warnings) rather than stopping at the first error.
func ValidateJobOffline(job *JobDefinition) *ValidationResult {
	result := &ValidationResult{}

	// 1. command field is required
	if job.Command == "" {
		result.Findings = append(result.Findings, ValidationFinding{
			Field:    "command",
			Severity: SeverityError,
			Message:  "'command' is required",
		})
	}

	// 2. environment field is required
	if job.Environment == "" {
		result.Findings = append(result.Findings, ValidationFinding{
			Field:    "environment",
			Severity: SeverityError,
			Message:  "'environment' is required",
		})
	}

	// 3. compute field is required
	if job.Compute == "" {
		result.Findings = append(result.Findings, ValidationFinding{
			Field:    "compute",
			Severity: SeverityError,
			Message:  "'compute' is required",
		})
	}

	// 4. code must not be a git path
	if job.Code != "" {
		lower := strings.ToLower(job.Code)
		if strings.HasPrefix(lower, "git://") || strings.HasPrefix(lower, "git+") {
			result.Findings = append(result.Findings, ValidationFinding{
				Field:    "code",
				Severity: SeverityError,
				Message:  fmt.Sprintf("git paths are not supported for 'code': '%s'. Use a local path instead", job.Code),
			})
		}
	}

	return result
}
