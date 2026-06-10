// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

var (
	warningPrefix = output.WithWarningFormat("(!) Warning:")
	failedPrefix  = output.WithErrorFormat("(x) Error:")
	skippedPrefix = output.WithGrayFormat("(-) Skipped:")
	donePrefix    = output.WithSuccessFormat("(✓) Done")
)

// ValidationReport renders the results of a validation pipeline run.
// It implements the ux.UxItem interface for display in the azd console.
type ValidationReport struct {
	Result *PipelineResult
}

// ToString renders the validation report as a formatted string for terminal display.
func (r *ValidationReport) ToString(currentIndentation string) string {
	if r.Result == nil || len(r.Result.GateResults) == 0 {
		return ""
	}

	var sb strings.Builder

	for i, gr := range r.Result.GateResults {
		if i > 0 {
			sb.WriteString("\n")
		}

		r.writeGateSection(&sb, currentIndentation, gr)
	}

	// Summary line
	sb.WriteString("\n")
	totalErrors := r.Result.TotalErrors()
	totalWarnings := r.Result.TotalWarnings()
	if totalErrors == 0 && totalWarnings == 0 {
		sb.WriteString(fmt.Sprintf(
			"%s%s Validation completed with no issues.\n",
			currentIndentation, donePrefix,
		))
	} else {
		sb.WriteString(fmt.Sprintf(
			"%sValidation completed: %d error(s), %d warning(s).\n",
			currentIndentation, totalErrors, totalWarnings,
		))
	}

	return sb.String()
}

// writeGateSection renders a single gate's results.
func (r *ValidationReport) writeGateSection(
	sb *strings.Builder, indent string, gr *GateResult,
) {
	gateHeader := output.WithBold("%s", gr.GateName)

	if gr.Skipped {
		sb.WriteString(fmt.Sprintf("%s%s %s", indent, skippedPrefix, gateHeader))
		if gr.SkipReason != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", gr.SkipReason))
		}
		sb.WriteString("\n")
		return
	}

	if len(gr.Results) == 0 {
		sb.WriteString(fmt.Sprintf(
			"%s%s %s - no issues found\n",
			indent, donePrefix, gateHeader,
		))
		return
	}

	sb.WriteString(fmt.Sprintf("%s%s\n", indent, gateHeader))

	// Partition into warnings and errors for consistent ordering
	var warnings, errors []CheckResult
	for _, cr := range gr.Results {
		if cr.Severity == CheckError {
			errors = append(errors, cr)
		} else {
			warnings = append(warnings, cr)
		}
	}

	for _, w := range warnings {
		writeCheckItem(sb, indent+"  ", warningPrefix, w)
	}
	for _, e := range errors {
		writeCheckItem(sb, indent+"  ", failedPrefix, e)
	}
}

// writeCheckItem renders a single check result with message, suggestion, and links.
func writeCheckItem(
	sb *strings.Builder, indent string, prefix string, item CheckResult,
) {
	if item.Message == "" {
		return
	}

	lines := strings.Split(item.Message, "\n")
	sb.WriteString(fmt.Sprintf("%s%s %s\n", indent, prefix, lines[0]))
	for _, line := range lines[1:] {
		sb.WriteString(fmt.Sprintf("%s%s\n", indent, line))
	}

	if item.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("%s%s %s\n",
			indent,
			output.WithHighLightFormat("Suggestion:"),
			item.Suggestion))
	}
	for _, link := range item.Links {
		if link.Title != "" {
			sb.WriteString(fmt.Sprintf("%s• %s\n",
				indent,
				output.WithHyperlink(link.URL, link.Title)))
		} else {
			sb.WriteString(fmt.Sprintf("%s• %s\n",
				indent,
				output.WithLinkFormat(link.URL)))
		}
	}
}

// MarshalJSON serializes the validation report for JSON output and telemetry.
func (r *ValidationReport) MarshalJSON() ([]byte, error) {
	if r.Result == nil {
		return json.Marshal(output.EventForMessage("validate: no results"))
	}

	type jsonGateResult struct {
		Gate     string `json:"gate"`
		Skipped  bool   `json:"skipped,omitempty"`
		Errors   int    `json:"errors"`
		Warnings int    `json:"warnings"`
	}

	gates := make([]jsonGateResult, len(r.Result.GateResults))
	for i, gr := range r.Result.GateResults {
		gates[i] = jsonGateResult{
			Gate:     gr.GateName,
			Skipped:  gr.Skipped,
			Errors:   gr.ErrorCount(),
			Warnings: gr.WarningCount(),
		}
	}

	return json.Marshal(struct {
		Type    string           `json:"type"`
		Gates   []jsonGateResult `json:"gates"`
		Summary string           `json:"summary"`
	}{
		Type:  "validate.report",
		Gates: gates,
		Summary: fmt.Sprintf("%d error(s), %d warning(s)",
			r.Result.TotalErrors(), r.Result.TotalWarnings()),
	})
}

// Ensure ValidationReport satisfies ux.UxItem at compile time.
var _ ux.UxItem = (*ValidationReport)(nil)
