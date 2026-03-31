// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
// yamlDir is the directory containing the YAML file, used to resolve relative paths.
// It returns all findings (errors and warnings) rather than stopping at the first error.
func ValidateJobOffline(job *JobDefinition, yamlDir string) *ValidationResult {
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

	// 5. Local path existence checks
	validateLocalPath(result, "code", job.Code, yamlDir)
	for name, input := range job.Inputs {
		if input.Value == "" {
			validateLocalPath(result, fmt.Sprintf("inputs.%s.path", name), input.Path, yamlDir)
		}
	}

	// 6. Validate ${{inputs.xxx}} and ${{outputs.xxx}} placeholders in command
	if job.Command != "" {
		validatePlaceholders(result, job)
	}

	// 7. Warn on single-brace {inputs.xxx} or {outputs.xxx} usage in command
	if job.Command != "" {
		validateSingleBracePlaceholders(result, job.Command)
	}

	// 8. Inputs/outputs with nil/empty definitions referenced in command
	if job.Command != "" {
		validateInputOutputDefinitions(result, job)
	}

	return result
}

// validateLocalPath checks that a local path exists on disk.
// Remote URIs (azureml://, https://, http://) and empty paths are skipped.
func validateLocalPath(result *ValidationResult, field string, path string, yamlDir string) {
	if path == "" || IsRemoteURI(path) {
		return
	}

	// Resolve relative paths against the YAML file directory
	resolved := path
	if !filepath.IsAbs(path) {
		resolved = filepath.Join(yamlDir, path)
	}

	if _, err := os.Stat(resolved); os.IsNotExist(err) {
		result.Findings = append(result.Findings, ValidationFinding{
			Field:    field,
			Severity: SeverityError,
			Message:  fmt.Sprintf("local path does not exist: '%s'", path),
		})
	}
}

// Regex patterns for placeholder validation.
var (
	// Matches ${{inputs.key}} or ${{outputs.key}} — captures "inputs" or "outputs" and the key name.
	placeholderRegex = regexp.MustCompile(`\$\{\{(inputs|outputs)\.(\w[\w.-]*)}}`)

	// Matches optional blocks: [...] (content between square brackets).
	optionalBlockRegex = regexp.MustCompile(`\[[^\]]*]`)

	// Matches ${{inputs.key}} — used to extract input keys from optional blocks.
	inputPlaceholderRegex = regexp.MustCompile(`\$\{\{inputs\.(\w[\w.-]*)}}`)

	// Matches single-brace {inputs.key} or {outputs.key} that are NOT preceded by $ or another {.
	// Uses a negative lookbehind approximation: we check matches and filter in code.
	singleBraceRegex = regexp.MustCompile(`\{(inputs|outputs)\.(\w[\w.-]*)}}?`)
)

// validatePlaceholders checks that ${{inputs.xxx}} references in command exist in job.Inputs
// and ${{outputs.xxx}} references exist in job.Outputs.
// References inside [...] optional blocks are skipped for inputs.
func validatePlaceholders(result *ValidationResult, job *JobDefinition) {
	command := job.Command

	// Build set of optional input keys (those inside [...] blocks)
	optionalInputs := make(map[string]bool)
	for _, block := range optionalBlockRegex.FindAllString(command, -1) {
		for _, match := range inputPlaceholderRegex.FindAllStringSubmatch(block, -1) {
			optionalInputs[match[1]] = true
		}
	}

	// Find all ${{inputs.xxx}} and ${{outputs.xxx}} references
	for _, match := range placeholderRegex.FindAllStringSubmatch(command, -1) {
		kind := match[1] // "inputs" or "outputs"
		key := match[2]

		// Only validate input placeholders — outputs are auto-provisioned by the backend
		if kind == "inputs" {
			if optionalInputs[key] {
				continue // skip optional inputs
			}
			if job.Inputs == nil {
				result.Findings = append(result.Findings, ValidationFinding{
					Field:    "command",
					Severity: SeverityError,
					Message:  fmt.Sprintf("command references '${{inputs.%s}}' but no inputs are defined", key),
				})
			} else if _, exists := job.Inputs[key]; !exists {
				result.Findings = append(result.Findings, ValidationFinding{
					Field:    "command",
					Severity: SeverityError,
					Message:  fmt.Sprintf("command references '${{inputs.%s}}' but '%s' is not defined in inputs", key, key),
				})
			}
		}
	}
}

// validateSingleBracePlaceholders flags when the command uses {inputs.xxx} or {outputs.xxx}
// instead of the correct ${{inputs.xxx}} syntax. This is an error because the backend
// will not resolve single-brace placeholders.
func validateSingleBracePlaceholders(result *ValidationResult, command string) {
	for _, match := range singleBraceRegex.FindAllStringSubmatchIndex(command, -1) {
		start := match[0]
		// Skip if this is already part of a ${{...}} (preceded by "${")
		if start >= 2 && command[start-2:start] == "${" {
			continue
		}
		if start >= 1 && command[start-1:start] == "$" {
			continue
		}

		kind := command[match[2]:match[3]]
		key := command[match[4]:match[5]]
		result.Findings = append(result.Findings, ValidationFinding{
			Field:    "command",
			Severity: SeverityError,
			Message:  fmt.Sprintf("command uses single-brace '{%s.%s}' — use '${{%s.%s}}' instead", kind, key, kind, key),
		})
	}
}

// validateInputOutputDefinitions checks that inputs/outputs referenced in command
// are not empty/nil definitions (all fields zero-valued).
// Empty inputs are errors; empty outputs are warnings (backend uses defaults).
func validateInputOutputDefinitions(result *ValidationResult, job *JobDefinition) {
	command := job.Command

	for _, match := range placeholderRegex.FindAllStringSubmatch(command, -1) {
		kind := match[1]
		key := match[2]

		if kind == "inputs" && job.Inputs != nil {
			if input, exists := job.Inputs[key]; exists {
				if (input == InputDefinition{}) {
					result.Findings = append(result.Findings, ValidationFinding{
						Field:    fmt.Sprintf("inputs.%s", key),
						Severity: SeverityError,
						Message:  fmt.Sprintf("input '%s' is referenced in command but has an empty definition", key),
					})
				}
			}
		} else if kind == "outputs" && job.Outputs != nil {
			if output, exists := job.Outputs[key]; exists {
				if (output == OutputDefinition{}) {
					result.Findings = append(result.Findings, ValidationFinding{
						Field:    fmt.Sprintf("outputs.%s", key),
						Severity: SeverityWarning,
						Message:  fmt.Sprintf("output '%s' has an empty definition — default values will be used", key),
					})
				}
			}
		}
	}
}
