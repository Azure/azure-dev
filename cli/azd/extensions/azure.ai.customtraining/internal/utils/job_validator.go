// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/fatih/color"
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

	// 1–3. Check required fields via struct tags
	// Note: only validates string fields. v.Field(i).String() returns "<T Value>" for non-string types,
	// so adding validate:"required" to a non-string field will silently pass.
	v := reflect.ValueOf(*job)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Tag.Get("validate") == "required" {
			if v.Field(i).String() == "" {
				result.Findings = append(result.Findings, ValidationFinding{
					Field:    field.Tag.Get("yaml"),
					Severity: SeverityError,
					Message:  "required field is missing",
				})
			}
		}
	}

	// 4. code must not be a git path
	if job.Code != "" {
		lower := strings.ToLower(job.Code)
		if strings.HasPrefix(lower, "git://") || strings.HasPrefix(lower, "git+") {
			result.Findings = append(result.Findings, ValidationFinding{
				Field:    "code",
				Severity: SeverityError,
				Message:  "git paths are not supported",
			})
		}
	}

	// 5. Local path existence checks + input type required.
	// Inputs with `value:` are literals (type defaults to "literal" at submit
	// time); inputs without `value:` carry a path/URI and must declare a type
	// — otherwise the backend rejects with "Unexpected JobInputType in request body: []".
	validateLocalPath(result, "code", job.Code, yamlDir)
	for name, input := range job.Inputs {
		if input.Value == "" {
			validateLocalPath(result, fmt.Sprintf("inputs.%s.path", name), input.Path, yamlDir)
			if strings.TrimSpace(input.Type) == "" {
				result.Findings = append(result.Findings, ValidationFinding{
					Field:    fmt.Sprintf("inputs.%s.type", name),
					Severity: SeverityError,
					Message:  "type is required (e.g. uri_folder, uri_file etc)",
				})
			}
		}
	}

	// 6–8. Command-level validation: placeholders, single-brace typos, empty definitions
	if job.Command != "" {
		optionalInputs := optionalInputKeys(job.Command)
		validatePlaceholders(result, job, optionalInputs)
		validateSingleBracePlaceholders(result, job.Command)
		validateInputOutputDefinitions(result, job, optionalInputs)
	}

	// 9. Outputs:
	//    a) "default" is reserved by the backend and rejected at submit time
	//       with a 400 ("Name of the output \"default\" is reserved by the system").
	//    b) Each declared output must have a non-empty type — the backend rejects
	//       missing/empty type with "Unexpected JobOutputType in request body: []".
	//    Catch both offline so users don't have to wait for the backend round-trip.
	for name, output := range job.Outputs {
		if strings.EqualFold(name, "default") {
			result.Findings = append(result.Findings, ValidationFinding{
				Field:    fmt.Sprintf("outputs.%s", name),
				Severity: SeverityError,
				Message:  "output name 'default' is reserved by the system; use a different name",
			})
		}
		if strings.TrimSpace(output.Type) == "" {
			result.Findings = append(result.Findings, ValidationFinding{
				Field:    fmt.Sprintf("outputs.%s.type", name),
				Severity: SeverityError,
				Message:  "type is required (e.g. uri_folder, uri_file etc)",
			})
		}
	}

	// 10. Services: only "ssh" is supported, and ssh_public_keys is required.
	// The backend currently does not enforce key presence — without keys the SSH
	// service is provisioned but unusable, and users hit the failure later.
	for name, svc := range job.Services {
		if !strings.EqualFold(svc.Type, "ssh") {
			result.Findings = append(result.Findings, ValidationFinding{
				Field:    fmt.Sprintf("services.%s.type", name),
				Severity: SeverityError,
				Message:  fmt.Sprintf("type %q is not supported; only 'ssh' is allowed", svc.Type),
			})
			continue
		}
		if strings.TrimSpace(svc.SshPublicKeys) == "" {
			result.Findings = append(result.Findings, ValidationFinding{
				Field:    fmt.Sprintf("services.%s.ssh_public_keys", name),
				Severity: SeverityError,
				Message:  "ssh_public_keys is required when type is 'ssh'",
			})
		}
	}

	return result
}

// ReportValidationResult prints findings to stdout and returns an error if any
// finding has Error severity. Shared by the `job validate` command (which calls
// it as the entire command body) and the `job submit` command (which calls it
// as a pre-flight check before any network or upload work).
//
// When printSuccess is true, a green success line is printed for clean and
// warnings-only results. Submit passes false so the success message doesn't
// clutter its own "Submitting command job…" output flow.
func ReportValidationResult(filePath string, result *ValidationResult, printSuccess bool) error {
	if len(result.Findings) == 0 {
		if printSuccess {
			color.Green("✓ Validation passed: %s\n", filePath)
		}
		return nil
	}

	fmt.Printf("Validation results for: %s\n\n", filePath)

	for _, f := range result.Findings {
		prefix := "⚠"
		if f.Severity == SeverityError {
			prefix = "✗"
		}
		fmt.Printf("  %s [%s] %s: %s\n", prefix, f.Severity, f.Field, f.Message)
	}

	fmt.Println()
	fmt.Printf("  Errors: %d, Warnings: %d\n", result.ErrorCount(), result.WarningCount())

	if result.HasErrors() {
		return fmt.Errorf("validation failed with %d error(s)", result.ErrorCount())
	}

	if printSuccess {
		color.Green("\n✓ Validation passed with warnings.\n")
	}
	return nil
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
	} else if err != nil {
		result.Findings = append(result.Findings, ValidationFinding{
			Field:    field,
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("could not verify path exists: '%s': %v", path, err),
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

// optionalInputKeys returns the set of input keys that appear exclusively inside [...] optional blocks.
// Keys that also appear outside brackets are not considered optional.
func optionalInputKeys(command string) map[string]bool {
	result := make(map[string]bool)
	for _, block := range optionalBlockRegex.FindAllString(command, -1) {
		for _, match := range inputPlaceholderRegex.FindAllStringSubmatch(block, -1) {
			result[match[1]] = true
		}
	}

	// Remove keys that also appear outside [...] blocks — those usages are required.
	stripped := optionalBlockRegex.ReplaceAllString(command, "")
	for _, match := range inputPlaceholderRegex.FindAllStringSubmatch(stripped, -1) {
		delete(result, match[1])
	}

	return result
}

// validatePlaceholders checks that ${{inputs.xxx}} references in command exist in job.Inputs
// and ${{outputs.xxx}} references exist in job.Outputs.
// References inside [...] optional blocks are skipped for inputs.
func validatePlaceholders(result *ValidationResult, job *JobDefinition, optionalInputs map[string]bool) {
	command := job.Command
	seen := make(map[string]bool)

	// Find all ${{inputs.xxx}} and ${{outputs.xxx}} references
	for _, match := range placeholderRegex.FindAllStringSubmatch(command, -1) {
		kind := match[1] // "inputs" or "outputs"
		key := match[2]

		dedupeKey := kind + "." + key
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true

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
	seen := make(map[string]bool)
	for _, match := range singleBraceRegex.FindAllStringSubmatchIndex(command, -1) {
		start := match[0]
		// Skip if this is already part of a ${{...}} (preceded by "${")
		if start >= 2 && command[start-2:start] == "${" {
			continue
		}

		kind := command[match[2]:match[3]]
		key := command[match[4]:match[5]]

		dedupeKey := kind + "." + key
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true

		result.Findings = append(result.Findings, ValidationFinding{
			Field:    "command",
			Severity: SeverityError,
			Message:  fmt.Sprintf("Incorrect placeholder format — use '${{%s.%s}}' instead", kind, key),
		})
	}
}

// validateInputOutputDefinitions checks that inputs/outputs referenced in command
// validateInputOutputDefinitions verifies that inputs referenced in the command
// are not empty/nil definitions (all fields zero-valued).
// Inputs inside [...] optional blocks are skipped — empty definitions are valid for optional inputs.
// Outputs are validated separately in ValidateJobOffline (rule 9).
func validateInputOutputDefinitions(result *ValidationResult, job *JobDefinition, optionalInputs map[string]bool) {
	command := job.Command
	seen := make(map[string]bool)

	for _, match := range placeholderRegex.FindAllStringSubmatch(command, -1) {
		kind := match[1]
		key := match[2]

		if kind != "inputs" || job.Inputs == nil {
			continue
		}

		dedupeKey := kind + "." + key
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true

		if optionalInputs[key] {
			continue
		}
		if input, exists := job.Inputs[key]; exists {
			if (input == InputDefinition{}) {
				result.Findings = append(result.Findings, ValidationFinding{
					Field:    fmt.Sprintf("inputs.%s", key),
					Severity: SeverityError,
					Message:  fmt.Sprintf("input '%s' is referenced in command but has an empty definition", key),
				})
			}
		}
	}
}
