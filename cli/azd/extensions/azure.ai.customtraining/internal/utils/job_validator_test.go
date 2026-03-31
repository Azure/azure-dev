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

// Tests ${{inputs.xxx}} and ${{outputs.xxx}} placeholder validation in command.
func TestValidate_PlaceholderMapping(t *testing.T) {
	// YAML — all placeholders map correctly:
	//   command: python train.py --data ${{inputs.training_data}} --out ${{outputs.model}}
	//   inputs:
	//     training_data:
	//       type: uri_folder
	//       path: azureml://datastores/blob/data
	//   outputs:
	//     model:
	//       type: uri_folder
	job := validJob()
	job.Command = "python train.py --data ${{inputs.training_data}} --out ${{outputs.model}}"
	job.Inputs = map[string]InputDefinition{
		"training_data": {Type: "uri_folder", Path: "azureml://datastores/blob/data"},
	}
	job.Outputs = map[string]OutputDefinition{"model": {Type: "uri_folder"}}
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "not defined"); f != nil {
		t.Errorf("did not expect error for mapped placeholders: %s", f.Message)
	}

	// YAML — typos in placeholder keys:
	//   command: >-
	//     python train.py
	//     --data ${{inputs.training_data}}
	//     --val ${{inputs.validation_data}}     ← "validation_data" NOT in inputs → error
	//     --out ${{outputs.model_output}}        ← "model_output" NOT in outputs → warning
	//   inputs:
	//     training_data:
	//       type: uri_folder
	//       path: azureml://datastores/blob/data
	//   outputs:
	//     model:                                  ← key is "model", not "model_output"
	//       type: uri_folder
	job = validJob()
	job.Command = "python train.py --data ${{inputs.training_data}} --val ${{inputs.validation_data}} --out ${{outputs.model_output}}"
	job.Inputs = map[string]InputDefinition{
		"training_data": {Type: "uri_folder", Path: "azureml://datastores/blob/data"},
	}
	job.Outputs = map[string]OutputDefinition{"model": {Type: "uri_folder"}}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "'validation_data' is not defined in inputs"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for unmapped input 'validation_data'")
	}
	if f := findFindingByMessage(result, "'model_output' is not defined in outputs"); f == nil || f.Severity != SeverityWarning {
		t.Error("expected warning for unmapped output 'model_output'")
	}

	// YAML — placeholders but no inputs/outputs section at all:
	//   command: python train.py --data ${{inputs.data}} --out ${{outputs.model}}
	//   (no inputs: or outputs: defined)
	job = validJob()
	job.Command = "python train.py --data ${{inputs.data}} --out ${{outputs.model}}"
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "no inputs are defined"); f == nil {
		t.Error("expected error when inputs section missing entirely")
	}
	if f := findFindingByMessage(result, "no outputs are defined"); f == nil {
		t.Error("expected warning when outputs section missing entirely")
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
}

// Tests single-brace {inputs.xxx} is flagged as error (backend won't resolve it).
func TestValidate_SingleBracePlaceholders(t *testing.T) {
	// YAML (incorrect):
	//   command: python train.py --data {inputs.training_data} --out {outputs.model}
	// Should be: ${{inputs.training_data}} and ${{outputs.model}}
	job := validJob()
	job.Command = "python train.py --data {inputs.training_data} --out {outputs.model}"
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "single-brace '{inputs.training_data}'"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for single-brace input placeholder")
	}
	if f := findFindingByMessage(result, "single-brace '{outputs.model}'"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for single-brace output placeholder")
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
	if f := findFindingByMessage(result, "single-brace"); f != nil {
		t.Error("did not expect single-brace error for correct ${{...}} syntax")
	}
}

// Tests input with empty definition (equivalent to Python None) is flagged.
func TestValidate_EmptyInputDefinition(t *testing.T) {
	// YAML — input key exists but has no properties (None):
	//   command: python train.py --data ${{inputs.training_data}}
	//   inputs:
	//     training_data:       ← key present but empty definition
	job := validJob()
	job.Command = "python train.py --data ${{inputs.training_data}}"
	job.Inputs = map[string]InputDefinition{"training_data": {}}
	result := ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "has an empty definition"); f == nil || f.Severity != SeverityError {
		t.Error("expected error for empty input definition")
	}

	// YAML — inputs with at least one field set are NOT empty:
	//   inputs:
	//     data1: { type: uri_folder }
	//     epochs: { value: "10" }
	//     data2: { path: azureml://datastores/blob/data }
	for _, input := range []InputDefinition{{Type: "uri_folder"}, {Value: "10"}, {Path: "azureml://x"}} {
		job = validJob()
		job.Command = "python train.py --x ${{inputs.x}}"
		job.Inputs = map[string]InputDefinition{"x": input}
		result = ValidateJobOffline(job, ".")
		if f := findFindingByMessage(result, "has an empty definition"); f != nil {
			t.Errorf("did not expect empty error for input %+v", input)
		}
	}

	// YAML — empty output definition shows warning (backend defaults to uri_folder + rw_mount):
	//   command: python train.py --out ${{outputs.model}}
	//   outputs:
	//     model:              ← empty definition → warning
	job = validJob()
	job.Command = "python train.py --out ${{outputs.model}}"
	job.Outputs = map[string]OutputDefinition{"model": {}}
	result = ValidateJobOffline(job, ".")
	if f := findFindingByMessage(result, "default values will be used"); f == nil || f.Severity != SeverityWarning {
		t.Error("expected warning for empty output definition")
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
