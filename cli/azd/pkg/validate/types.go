// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package validate provides the validation pipeline framework for azd.
//
// The framework is organized around three concepts:
//   - A [Pipeline] that orchestrates sequential execution of validation stages.
//   - [Gate] implementations that group related checks into a named stage.
//   - [Check] functions that perform individual validations within a gate.
//
// The pipeline flows a shared [PipelineContext] through each gate, allowing
// gates to read project/environment state and store computed data for
// downstream gates via the Values map.
package validate

import (
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// CheckSeverity indicates whether a check result is a warning or a blocking error.
type CheckSeverity int

const (
	// CheckWarning indicates a non-blocking issue that the user should be aware of.
	CheckWarning CheckSeverity = iota
	// CheckError indicates a blocking issue that should prevent deployment.
	CheckError
)

// CheckResult holds the outcome of a single validation check.
type CheckResult struct {
	// Severity indicates whether this result is a warning or a blocking error.
	Severity CheckSeverity
	// DiagnosticID is a unique, stable identifier for this finding type
	// (e.g. "role_assignment_missing"). Used in telemetry to correlate
	// findings with deployment outcomes.
	DiagnosticID string
	// Message is a human-readable description of the finding.
	Message string
	// Suggestion is an optional actionable recommendation for resolving the issue.
	Suggestion string
	// Links is an optional list of reference links related to the finding.
	Links []ux.PreflightReportLink
}

// GateResult aggregates results from all checks executed within a single gate.
type GateResult struct {
	// GateName is the unique identifier of the gate that produced these results.
	GateName string
	// Results contains the individual check outcomes. An empty non-nil slice
	// means checks ran but found nothing; a nil slice means the gate was skipped.
	Results []CheckResult
	// Skipped is true when the gate did not execute its checks
	// (e.g. required tooling was unavailable).
	Skipped bool
	// SkipReason explains why the gate was skipped. Only set when Skipped is true.
	SkipReason string
}

// HasErrors returns true if the gate result contains at least one error-level finding.
func (r *GateResult) HasErrors() bool {
	for _, result := range r.Results {
		if result.Severity == CheckError {
			return true
		}
	}
	return false
}

// HasWarnings returns true if the gate result contains at least one warning-level finding.
func (r *GateResult) HasWarnings() bool {
	for _, result := range r.Results {
		if result.Severity == CheckWarning {
			return true
		}
	}
	return false
}

// ErrorCount returns the number of error-level findings.
func (r *GateResult) ErrorCount() int {
	count := 0
	for _, result := range r.Results {
		if result.Severity == CheckError {
			count++
		}
	}
	return count
}

// WarningCount returns the number of warning-level findings.
func (r *GateResult) WarningCount() int {
	count := 0
	for _, result := range r.Results {
		if result.Severity == CheckWarning {
			count++
		}
	}
	return count
}

// PipelineResult aggregates results from all gates executed in the pipeline.
type PipelineResult struct {
	// GateResults holds the result for each gate that was executed.
	GateResults []*GateResult
}

// HasErrors returns true if any gate result contains at least one error-level finding.
func (r *PipelineResult) HasErrors() bool {
	for _, gr := range r.GateResults {
		if gr.HasErrors() {
			return true
		}
	}
	return false
}

// HasWarnings returns true if any gate result contains at least one warning-level finding.
func (r *PipelineResult) HasWarnings() bool {
	for _, gr := range r.GateResults {
		if gr.HasWarnings() {
			return true
		}
	}
	return false
}

// TotalErrors returns the total count of error-level findings across all gates.
func (r *PipelineResult) TotalErrors() int {
	total := 0
	for _, gr := range r.GateResults {
		total += gr.ErrorCount()
	}
	return total
}

// TotalWarnings returns the total count of warning-level findings across all gates.
func (r *PipelineResult) TotalWarnings() int {
	total := 0
	for _, gr := range r.GateResults {
		total += gr.WarningCount()
	}
	return total
}
