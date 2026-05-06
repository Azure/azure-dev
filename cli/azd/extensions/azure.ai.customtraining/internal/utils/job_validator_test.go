// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validJob() *JobDefinition {
	return &JobDefinition{
		Command:     "python train.py",
		Environment: "azureml:my-env:1",
		Compute:     "gpu-cluster",
	}
}

func findFindingByMessage(result *ValidationResult, substr string) *ValidationFinding {
	for _, f := range result.Findings {
		if strings.Contains(f.Message, substr) {
			return &f
		}
	}
	return nil
}

// Tests required fields and a fully valid job with all common YAML patterns.
func TestValidate_RequiredFieldsAndValidJob(t *testing.T) {
	// YAML with nothing — all required fields missing:
	//   (empty file)
	empty := &JobDefinition{}
	result := ValidateJobOffline(empty, ".")
	if result.ErrorCount() < 3 {
		t.Errorf("expected at least 3 errors (command, environment, compute), got %d", result.ErrorCount())
	}

	// Realistic valid YAML:
	//   command: >-
	//     python train.py
	//     --data ${{inputs.training_data}}
	//     --out ${{outputs.model}}
	//   environment: azureml://registries/azureml/environments/sklearn-1.5/labels/latest
	//   compute: azureml:gpu-cluster
	//   code: azureml://registries/mycode
	//   inputs:
	//     training_data:
	//       type: uri_folder
	//       path: azureml://datastores/workspaceblobstore/paths/data/train
	//   outputs:
	//     model:
	//       type: uri_folder
	job := validJob()
	job.Command = "python train.py --data ${{inputs.training_data}} --out ${{outputs.model}}"
	job.Code = "azureml://registries/mycode"
	job.Inputs = map[string]InputDefinition{
		"training_data": {Type: "uri_folder", Path: "azureml://datastores/workspaceblobstore/paths/data/train"},
	}
	job.Outputs = map[string]OutputDefinition{"model": {Type: "uri_folder"}}
	result = ValidateJobOffline(job, ".")
	if result.HasErrors() {
		for _, f := range result.Findings {
			t.Errorf("unexpected finding: [%s] %s: %s", f.Severity, f.Field, f.Message)
		}
	}
}

// Tests git code paths are rejected, normal code paths accepted.
func TestValidate_GitPaths(t *testing.T) {
	// YAML:  code: git://github.com/org/repo  — rejected
	// YAML:  code: git+https://github.com/org/repo  — rejected
	for _, code := range []string{"git://github.com/repo", "git+https://github.com/repo", "GIT://github.com/repo"} {
		job := validJob()
		job.Code = code
		result := ValidateJobOffline(job, ".")
		if f := findFindingByMessage(result, "git paths are not supported"); f == nil {
			t.Errorf("expected git path error for code=%q", code)
		}
	}

	// YAML:  code: ./src  — accepted (local)
	// YAML:  code: azureml://datastores/blob/paths/code  — accepted (remote)
	for _, code := range []string{"./src", "azureml://datastores/blob/paths/code"} {
		job := validJob()
		job.Code = code
		result := ValidateJobOffline(job, ".")
		if f := findFindingByMessage(result, "git paths are not supported"); f != nil {
			t.Errorf("did not expect git path error for code=%q", code)
		}
	}
}

// Tests local path existence for code and input paths.
func TestValidate_LocalPaths(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "src"), 0o755)

	// YAML:  code: src  — src/ exists on disk → no error
	job := validJob()
	job.Code = "src"
	result := ValidateJobOffline(job, dir)
	if f := findFindingByMessage(result, "local path does not exist"); f != nil {
		t.Error("did not expect error when src dir exists")
	}

	// YAML:  code: nonexistent  — does not exist → error
	job = validJob()
	job.Code = "nonexistent"
	result = ValidateJobOffline(job, dir)
	if f := findFindingByMessage(result, "local path does not exist: 'nonexistent'"); f == nil {
		t.Error("expected error for missing local code path")
	}

	// YAML:  code: azureml://datastores/blob/paths/src  — remote URI, skip check
	job = validJob()
	job.Code = "azureml://datastores/blob/paths/src"
	result = ValidateJobOffline(job, dir)
	if f := findFindingByMessage(result, "local path does not exist"); f != nil {
		t.Error("did not expect error for remote code path")
	}

	// YAML:
	//   inputs:
	//     training_data:
	//       type: uri_folder
	//       path: nonexistent_data        ← local path missing → error
	//     pretrained_model:
	//       type: uri_folder
	//       path: azureml://datastores/blob/data  ← remote → skipped
	//     epochs:
	//       value: "10"                   ← literal value, no path → skipped
	job = validJob()
	job.Inputs = map[string]InputDefinition{
		"training_data":    {Type: "uri_folder", Path: "nonexistent_data"},
		"pretrained_model": {Type: "uri_folder", Path: "azureml://datastores/blob/data"},
		"epochs":           {Value: "10"},
	}
	result = ValidateJobOffline(job, dir)
	if f := findFindingByMessage(result, "'nonexistent_data'"); f == nil {
		t.Error("expected error for missing input local path")
	}
	if f := findFindingByMessage(result, "pretrained_model"); f != nil {
		t.Error("did not expect error for remote input path")
	}
}

// Tests ${{inputs.xxx}} placeholder validation in command.
// Output placeholders are NOT validated here — outputs are auto-provisioned by the backend.
func TestValidate_PlaceholderMapping(t *testing.T) {
	// YAML — typo in input placeholder key:
	//   command: >-
	//     python train.py
	//     --data ${{inputs.training_data}}
	//     --val ${{inputs.validation_data}}     ← "validation_data" NOT in inputs → error
	//   inputs:
	//     training_data:
	//       type: uri_folder
	//       path: azureml://datastores/blob/data
	job := validJob()
	job.Command = "python train.py --data ${{inputs.training_data}} --val ${{inputs.validation_data}}"
	job.Inputs = map[string]InputDefinition{
		"training_data": {Type: "uri_folder", Path: "azureml://datastores/blob/data"},
	}
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "'validation_data' is not defined in inputs"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for unmapped input 'validation_data'")
	}

	// YAML — input placeholders but no inputs section at all:
	//   command: python train.py --data ${{inputs.data}}
	//   (no inputs: defined)
	job = validJob()
	job.Command = "python train.py --data ${{inputs.data}}"
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "no inputs are defined"); f == nil {
		t.Error("expected error when inputs section missing entirely")
	}

	// YAML — output placeholders but no outputs section at all:
	//   command: python train.py --out ${{outputs.model}}
	//   (no outputs: defined — backend auto-provisions, so no error or warning)
	job = validJob()
	job.Command = "python train.py --out ${{outputs.model}}"
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "outputs"); f != nil {
		t.Errorf("did not expect any output finding when outputs section missing: %s", f.Message)
	}

	// YAML — optional inputs inside [...] brackets:
	//   command: >-
	//     python train.py
	//     --data ${{inputs.training_data}}
	//     [--val ${{inputs.validation_data}} --lr ${{inputs.learning_rate}}]
	//   inputs:
	//     training_data:
	//       type: uri_folder
	//       path: azureml://datastores/blob/data
	//   (validation_data and learning_rate NOT defined — but inside [] so OK)
	job = validJob()
	job.Command = "python train.py --data ${{inputs.training_data}} [--val ${{inputs.validation_data}} --lr ${{inputs.learning_rate}}]"
	job.Inputs = map[string]InputDefinition{
		"training_data": {Type: "uri_folder", Path: "azureml://datastores/blob/data"},
	}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "validation_data"); f != nil {
		t.Error("did not expect error for optional validation_data inside brackets")
	}
	if f := findFindingByMessage(result, "learning_rate"); f != nil {
		t.Error("did not expect error for optional learning_rate inside brackets")
	}

	// YAML — key appears both inside and outside brackets:
	//   command: python train.py --data ${{inputs.data}} [--extra ${{inputs.data}}]
	//   (no inputs defined — data appears outside brackets so it's required → error)
	job = validJob()
	job.Command = "python train.py --data ${{inputs.data}} [--extra ${{inputs.data}}]"
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "no inputs are defined"); f == nil {
		t.Error("expected error when key appears both inside and outside brackets")
	}
}

// Tests single-brace {inputs.xxx} is flagged as error (backend won't resolve it).
func TestValidate_SingleBracePlaceholders(t *testing.T) {
	// YAML (incorrect):
	//   command: python train.py --data {inputs.training_data} --out {outputs.model}
	// Should be: ${{inputs.training_data}} and ${{outputs.model}}
	job := validJob()
	job.Command = "python train.py --data {inputs.training_data} --out {outputs.model}"
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "${{inputs.training_data}}"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for single-brace input placeholder")
	}
	if f := findFindingByMessage(result, "${{outputs.model}}"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for single-brace output placeholder")
	}

	// YAML (incorrect) — single-brace inside [...] brackets:
	//   command: python train.py [--data {inputs.optional_data}]
	// Single-brace is wrong syntax even inside optional blocks → still error
	job = validJob()
	job.Command = "python train.py [--data {inputs.optional_data}]"
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "${{inputs.optional_data}}"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for single-brace inside optional brackets")
	}

	// YAML (correct):
	//   command: python train.py --data ${{inputs.training_data}}
	// Correct ${{...}} should NOT trigger single-brace error
	job = validJob()
	job.Command = "python train.py --data ${{inputs.training_data}}"
	job.Inputs = map[string]InputDefinition{
		"training_data": {Type: "uri_folder", Path: "azureml://datastores/blob/data"},
	}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "Incorrect placeholder format"); f != nil {
		t.Error("did not expect single-brace error for correct ${{...}} syntax")
	}

	// YAML (incorrect) — dollar + single brace (regression test):
	//   command: python train.py --data ${inputs.data}
	// ${...} is wrong syntax — should be ${{...}} → error
	job = validJob()
	job.Command = "python train.py --data ${inputs.data}"
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "${{inputs.data}}"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for dollar-single-brace ${inputs.xxx}")
	}

	// YAML (incorrect) — duplicate single-brace placeholders should produce only one error:
	//   command: python train.py --data {inputs.data} --val {inputs.data}
	job = validJob()
	job.Command = "python train.py --data {inputs.data} --val {inputs.data}"
	result = ValidateJobOffline(job, ".")
	errorCount := 0
	for _, f := range result.Findings {
		if strings.Contains(f.Message, "${{inputs.data}}") {
			errorCount++
		}
	}
	if errorCount != 1 {
		t.Errorf("expected exactly 1 error for duplicate single-brace placeholder, got %d", errorCount)
	}
}

// Tests empty input/output definitions (equivalent to Python None) are flagged.
func TestValidate_EmptyDefinitions(t *testing.T) {
	// YAML — input key exists but has no properties (None):
	//   command: python train.py --data ${{inputs.training_data}}
	//   inputs:
	//     training_data:       ← key present but empty definition → error
	job := validJob()
	job.Command = "python train.py --data ${{inputs.training_data}}"
	job.Inputs = map[string]InputDefinition{"training_data": {}}
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "has an empty definition"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for empty input definition")
	}

	// YAML — output key exists but has no properties (None):
	//   command: python train.py --out ${{outputs.model}}
	//   outputs:
	//     model:              ← empty definition → error (backend rejects empty type)
	job = validJob()
	job.Command = "python train.py --out ${{outputs.model}}"
	job.Outputs = map[string]OutputDefinition{"model": {}}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "type is required"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for empty output definition (missing type)")
	}
}

// Tests multiline commands (YAML | and >- both resolve correctly after unmarshal).
func TestValidate_MultilineCommand(t *testing.T) {
	// YAML with pipe (|) — newlines preserved:
	//   command: |
	//     python train.py
	//     --data ${{inputs.training_data}}
	//     --out ${{outputs.model}}
	// After unmarshal: "python train.py\n--data ${{inputs.training_data}}\n--out ${{outputs.model}}\n"
	job := validJob()
	job.Command = "python train.py\n--data ${{inputs.training_data}}\n--out ${{outputs.model}}\n"
	job.Inputs = map[string]InputDefinition{
		"training_data": {Type: "uri_folder", Path: "azureml://datastores/blob/data"},
	}
	job.Outputs = map[string]OutputDefinition{"model": {}}
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "not defined"); f != nil {
		t.Errorf("did not expect error for multiline command: %s", f.Message)
	}
}

// Tests services block validation:
//   - non-ssh service type → error
//   - ssh service missing ssh_public_keys → error
//   - ssh service with keys → no findings for that service
func TestValidate_Services(t *testing.T) {
	// Unsupported service type:
	//   services:
	//     jupyter:
	//       type: jupyter_lab
	//       ssh_public_keys: ssh-rsa AAA...
	job := validJob()
	job.Services = map[string]ServiceDefinition{
		"jupyter": {Type: "jupyter_lab", SshPublicKeys: "ssh-rsa AAA..."},
	}
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "is not supported"); f == nil {
		t.Error("expected error for unsupported service type")
	} else if f.Severity != SeverityError {
		t.Errorf("expected SeverityError for unsupported service type, got %s", f.Severity)
	}

	// SSH service missing ssh_public_keys:
	//   services:
	//     my_ssh:
	//       type: ssh
	job = validJob()
	job.Services = map[string]ServiceDefinition{
		"my_ssh": {Type: "ssh"},
	}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "ssh_public_keys is required"); f == nil {
		t.Error("expected error for missing ssh_public_keys")
	} else if f.Severity != SeverityError {
		t.Errorf("expected SeverityError for missing ssh_public_keys, got %s", f.Severity)
	}

	// Whitespace-only ssh_public_keys also counts as missing:
	job = validJob()
	job.Services = map[string]ServiceDefinition{
		"my_ssh": {Type: "ssh", SshPublicKeys: "   \n  "},
	}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "ssh_public_keys is required"); f == nil {
		t.Error("expected error for whitespace-only ssh_public_keys")
	}

	// Valid SSH service — no service-related findings:
	job = validJob()
	job.Services = map[string]ServiceDefinition{
		"my_ssh": {Type: "ssh", SshPublicKeys: "ssh-rsa AAA..."},
	}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "ssh_public_keys"); f != nil {
		t.Errorf("did not expect ssh_public_keys finding for valid SSH service: %s", f.Message)
	}
	if f := findFindingByMessage(result, "is not supported"); f != nil {
		t.Errorf("did not expect type-not-supported finding for valid SSH service: %s", f.Message)
	}
}

// Tests that the reserved output name "default" is rejected. The backend rejects
// it at submit time with a 400; we catch it offline so users don't have to wait
// for the round-trip.
func TestValidate_ReservedOutputName(t *testing.T) {
	// Lowercase "default":
	//   outputs:
	//     default:
	//       type: uri_folder
	job := validJob()
	job.Outputs = map[string]OutputDefinition{"default": {Type: "uri_folder"}}
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "reserved by the system"); f == nil {
		t.Error("expected error for output named 'default'")
	} else if f.Severity != SeverityError {
		t.Errorf("expected SeverityError for reserved output name, got %s", f.Severity)
	}

	// Case-insensitive — "Default" should also be rejected:
	job = validJob()
	job.Outputs = map[string]OutputDefinition{"Default": {Type: "uri_folder"}}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "reserved by the system"); f == nil {
		t.Error("expected error for output named 'Default' (case-insensitive)")
	}

	// Non-reserved name — no reserved-name finding:
	job = validJob()
	job.Outputs = map[string]OutputDefinition{"model": {Type: "uri_folder"}}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "reserved by the system"); f != nil {
		t.Errorf("did not expect reserved-name finding for output 'model': %s", f.Message)
	}

	// Inputs named "default" are NOT reserved — should be allowed:
	job = validJob()
	job.Inputs = map[string]InputDefinition{"default": {Type: "uri_folder", Path: "azureml://datastore/x"}}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "reserved by the system"); f != nil {
		t.Errorf("did not expect reserved-name finding for input 'default': %s", f.Message)
	}
}

// Tests that path-style inputs (no `value:`) must declare a type. The backend
// rejects missing/empty input type with "Unexpected JobInputType in request body: []".
func TestValidate_InputTypeRequired(t *testing.T) {
	// Path input missing type:
	//   inputs:
	//     train_data:
	//       path: azureml://datastore/x   ← no type → error
	job := validJob()
	job.Inputs = map[string]InputDefinition{
		"train_data": {Path: "azureml://datastore/x"},
	}
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "type is required"); f == nil {
		t.Error("expected error for path input missing type")
	} else if f.Severity != SeverityError {
		t.Errorf("expected SeverityError for path input missing type, got %s", f.Severity)
	}

	// Literal input (value: set) — type defaults to "literal", no error expected:
	//   inputs:
	//     epochs:
	//       value: "10"
	job = validJob()
	job.Inputs = map[string]InputDefinition{
		"epochs": {Value: "10"},
	}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "type is required"); f != nil {
		t.Errorf("did not expect type-required finding for literal input: %s", f.Message)
	}

	// Path input with type — no finding:
	job = validJob()
	job.Inputs = map[string]InputDefinition{
		"train_data": {Type: "uri_folder", Path: "azureml://datastore/x"},
	}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "type is required"); f != nil {
		t.Errorf("did not expect type-required finding for valid path input: %s", f.Message)
	}
}

func TestValidationResult_HasErrorsAndCounts(t *testing.T) {
	r := &ValidationResult{}
	if r.HasErrors() {
		t.Error("expected no errors on empty result")
	}
	r.Findings = append(r.Findings, ValidationFinding{Severity: SeverityWarning})
	if r.HasErrors() {
		t.Error("warnings should not count as errors")
	}
	r.Findings = append(r.Findings, ValidationFinding{Severity: SeverityError}, ValidationFinding{Severity: SeverityError})
	if r.ErrorCount() != 2 || r.WarningCount() != 1 {
		t.Errorf("expected 2 errors, 1 warning, got %d errors, %d warnings", r.ErrorCount(), r.WarningCount())
	}
}
