// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/require"
)

type sourcePlannedInput struct {
	Path         *string `json:"path"`
	SourceRoot   *string `json:"source_root"`
	RelativePath string  `json:"relative_path"`
	Origin       string  `json:"origin"`
	SizeBytes    *int64  `json:"size_bytes"`
	URL          *string `json:"url"`
}

type sourceInputPlan struct {
	Inputs          []sourcePlannedInput `json:"inputs"`
	Mode            string               `json:"mode"`
	Recursive       bool                 `json:"recursive"`
	Pattern         *string              `json:"pattern"`
	TotalBytes      int64                `json:"total_bytes"`
	ExtensionCounts map[string]int       `json:"extension_counts"`
	Skipped         []struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
	} `json:"skipped"`
}

type outputPlanningCall struct {
	Operation string `json:"operation"`
	Input     struct {
		Args   []sourceInputPlan `json:"args"`
		Kwargs struct {
			View       string  `json:"view"`
			OutputFile *string `json:"output_file"`
			OutputDir  *string `json:"output_dir"`
			OnExisting string  `json:"on_existing"`
		} `json:"kwargs"`
	} `json:"input"`
	Filesystem    []discoveryFile `json:"filesystem"`
	Result        json.RawMessage `json:"result"`
	ResultType    string          `json:"result_type"`
	Error         json.RawMessage `json:"error"`
	NativeResult  json.RawMessage `json:"native_result"`
	NativeError   *discoveryError `json:"native_error"`
	NativePreview json.RawMessage `json:"native_preview"`
}

type outputPlanningCase struct {
	SourceCase string               `json:"source_case"`
	Calls      []outputPlanningCall `json:"calls"`
}

type outputPlanningOracle struct {
	SchemaVersion int    `json:"schema_version"`
	Role          string `json:"role"`
	Source        struct {
		Repository    string            `json:"repository"`
		Commit        string            `json:"commit"`
		License       string            `json:"license"`
		Python        string            `json:"python"`
		Platform      string            `json:"platform"`
		Files         map[string]string `json:"files"`
		Functions     []string          `json:"functions"`
		CaseCount     int               `json:"case_count"`
		CallCount     int               `json:"call_count"`
		DeferredCases map[string]string `json:"deferred_cases"`
	} `json:"source"`
	Boundaries []string             `json:"boundaries"`
	Cases      []outputPlanningCase `json:"cases"`
}

const outputPlanningGoTest = "internal/cmd/output_planning_parity_test.go::TestOutputPlanningParity"

func loadOutputPlanningOracle(t *testing.T) outputPlanningOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/output_planning.json", inventory.OutputPlanning.File)
	body, err := os.ReadFile("../../testdata/parity/output_planning.json")
	require.NoError(t, err)
	require.Equal(t, inventory.OutputPlanning.SHA256, sha256Text(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle outputPlanningOracle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&oracle))
	require.Equal(t, 1, oracle.SchemaVersion)
	require.Equal(t, "public-source-test-controls-not-service-recordings", oracle.Role)
	require.Equal(t, inventory.Source.Repository, oracle.Source.Repository)
	require.Equal(t, paritySourceCommit, oracle.Source.Commit)
	require.Equal(t, "MIT", oracle.Source.License)
	require.Equal(t, "Linux", oracle.Source.Platform)
	require.NotEmpty(t, oracle.Source.Python)
	require.Len(t, oracle.Source.Files, 5)
	for _, path := range []string{
		"packages/core/tests/test_input_planning.py",
		"packages/core/src/cu_cli_core/input_planning.py",
		"packages/core/src/cu_cli_core/contracts.py",
		"packages/core/src/cu_cli_core/errors.py",
		"packages/core/src/cu_cli_core/serialization.py",
	} {
		require.Regexp(t, "^[0-9a-f]{64}$", oracle.Source.Files[path])
	}
	require.Len(t, oracle.Source.Functions, 16)
	require.Len(t, oracle.Boundaries, 4)
	require.Len(t, oracle.Source.DeferredCases, 6)
	require.Equal(t, 83, oracle.Source.CaseCount)
	require.Equal(t, 97, oracle.Source.CallCount)
	require.Len(t, oracle.Cases, oracle.Source.CaseCount)
	mapped := map[string]bool{}
	for _, row := range inventory.Cases {
		if slices.ContainsFunc(row.GoTests, func(reference string) bool {
			return strings.HasPrefix(reference, outputPlanningGoTest+"/")
		}) {
			require.Contains(t, []string{"evidenced", "approved"}, row.State)
			require.Equal(t, "difference", row.Disposition)
			require.NoError(t, parityState(row.State, row.Approval))
			require.Contains(t, row.GoTests, outputPlanningGoTest+"/"+inputPlanningSubtest(row.Source))
			mapped[row.Source] = true
		}
	}
	require.Len(t, mapped, oracle.Source.CaseCount)
	calls, nativeErrors, earlyExistingErrors := 0, 0, 0
	for _, row := range oracle.Cases {
		require.True(t, mapped[row.SourceCase], "missing/duplicate source mapping: %s", row.SourceCase)
		delete(mapped, row.SourceCase)
		require.NotContains(t, oracle.Source.DeferredCases, row.SourceCase)
		name, found := strings.CutPrefix(row.SourceCase, "packages/core/tests/test_input_planning.py::")
		require.True(t, found)
		function, _, _ := strings.Cut(name, "[")
		require.Contains(t, oracle.Source.Functions, function)
		require.NotEmpty(t, row.Calls)
		for _, call := range row.Calls {
			calls++
			require.Equal(t, "plan_outputs", call.Operation)
			require.Len(t, call.Input.Args, 1)
			require.NotEmpty(t, call.Input.Args[0].Inputs)
			require.Contains(t, []string{"full", "llm-input"}, call.Input.Kwargs.View)
			if string(call.Error) == "null" {
				require.Equal(t, "cu_cli_core.contracts.ExecutionPlan", call.ResultType)
				require.NotEqual(t, "null", string(call.Result))
			} else {
				require.Empty(t, call.ResultType)
				require.Equal(t, "null", string(call.Result))
				require.NotNil(t, call.NativeError)
			}
			if call.NativeError != nil {
				nativeErrors++
				require.Equal(t, "null", string(call.NativeResult))
				require.Equal(t, "null", string(call.NativePreview))
				if string(call.Error) == "null" {
					earlyExistingErrors++
					require.Equal(t, "result_exists", call.NativeError.Code)
				}
			} else {
				require.NotEqual(t, "null", string(call.NativeResult))
				require.NotEqual(t, "null", string(call.NativePreview))
			}
		}
	}
	require.Empty(t, mapped)
	require.Equal(t, oracle.Source.CallCount, calls)
	require.Equal(t, 54, nativeErrors)
	require.Equal(t, 3, earlyExistingErrors)
	return oracle
}

func sourcePlanInputs(t *testing.T, root string, plan sourceInputPlan) []analysis.Input {
	t.Helper()
	result := make([]analysis.Input, 0, len(plan.Inputs))
	for _, source := range plan.Inputs {
		var input analysis.Input
		if source.URL != nil {
			require.Nil(t, source.Path)
			require.Nil(t, source.SourceRoot)
			require.Nil(t, source.SizeBytes)
			inputs, err := analysis.Discover(t.Context(), analysis.InputOptions{URLs: []string{*source.URL}})
			require.NoError(t, err)
			require.Len(t, inputs, 1)
			input = inputs[0]
		} else {
			require.NotNil(t, source.Path)
			require.NotNil(t, source.SourceRoot)
			require.NotNil(t, source.SizeBytes)
			path := expandPlanningPath(t, root, *source.Path)
			var err error
			input, err = analysis.DiscoverFile(t.Context(), path)
			require.NoError(t, err)
			relative, err := filepath.Rel(expandPlanningPath(t, root, *source.SourceRoot), input.Path)
			require.NoError(t, err)
			require.Equal(t, source.RelativePath, filepath.ToSlash(relative))
			input.Relative = relative
			reader, err := input.Open()
			require.NoError(t, err)
			content, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			require.NoError(t, readErr)
			require.NoError(t, closeErr)
			require.Equal(t, *source.SizeBytes, int64(len(content)))
		}
		require.Equal(t, source.RelativePath, filepath.ToSlash(input.Relative))
		result = append(result, input)
	}
	return result
}

func expandPlanningPath(t *testing.T, root, value string) string {
	t.Helper()
	if value == "$TMP" {
		return root
	}
	require.True(t, strings.HasPrefix(value, "$TMP/"), "path outside the source temporary root")
	return discoveryPath(t, root, strings.TrimPrefix(value, "$TMP/"))
}

type outputPlanningInput struct {
	Path      string `json:"path"`
	URL       string `json:"url"`
	Reference string `json:"reference"`
	Relative  string `json:"relative"`
}

type outputPlanningOutput struct {
	Input   outputPlanningInput `json:"input"`
	Path    *string             `json:"path"`
	Exists  bool                `json:"exists"`
	Skipped bool                `json:"skipped"`
}

type outputPlanningResult struct {
	Outputs  []outputPlanningOutput `json:"outputs"`
	Existing string                 `json:"existing"`
	Report   string                 `json:"report"`
	LLMInput bool                   `json:"llm_input"`
}

func executeOutputPlanning(t *testing.T, call outputPlanningCall) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	executeOutputPlanningAt(t, root, call)
}

func executeOutputPlanningAt(t *testing.T, root string, call outputPlanningCall) analysis.Plan {
	t.Helper()
	fixture := materializeDiscovery(t, root, call.Filesystem)
	require.Equal(t, call.Filesystem, discoverySnapshot(t, fixture))
	t.Chdir(root)
	inputs := sourcePlanInputs(t, root, call.Input.Args[0])
	options := call.Input.Kwargs
	existing := options.OnExisting
	if existing == "" {
		existing = "error"
	}
	nativeOptions := analysis.OutputOptions{Existing: existing, LLMInput: options.View == "llm-input"}
	if options.OutputFile != nil {
		nativeOptions.File = expandPlanningPath(t, root, *options.OutputFile)
	}
	if options.OutputDir != nil {
		nativeOptions.Directory = expandPlanningPath(t, root, *options.OutputDir)
	}
	actual, err := analysis.PlanOutputs(inputs, nativeOptions)
	require.Equal(t, call.Filesystem, discoverySnapshot(t, fixture), "native planner mutated the fixture tree")
	if call.NativeError != nil {
		require.Error(t, err)
		require.Equal(t, analysis.Plan{}, actual)
		var domain *cuclient.Error
		require.True(t, errors.As(err, &domain))
		require.Equal(t, *call.NativeError, discoveryError{
			Kind: string(domain.Kind), Code: domain.Code, Message: domain.Message,
			Hint: domain.Suggestion, StatusCode: domain.StatusCode,
		})
		require.NotContains(t, err.Error(), "secret")
		if domain.Code == "output_name_too_long" {
			require.Len(t, inputs, 1)
			suffix := ".result.json"
			if nativeOptions.LLMInput {
				suffix = ".result.md"
			}
			require.Greater(t, len(filepath.Base(inputs[0].Relative)+suffix), 240)
		}
		return actual
	}
	require.NoError(t, err)
	normalize := func(value string) string {
		return strings.ReplaceAll(filepath.ToSlash(value), filepath.ToSlash(root), "$TMP")
	}
	observed := outputPlanningResult{
		Existing: actual.Existing, Report: actual.Report, LLMInput: actual.LLMInput,
	}
	for _, output := range actual.Outputs {
		row := outputPlanningOutput{
			Input: outputPlanningInput{
				Path: normalize(output.Input.Path), URL: output.Input.URL,
				Reference: normalize(output.Input.Reference), Relative: filepath.ToSlash(output.Input.Relative),
			},
			Exists: output.Exists, Skipped: output.Skipped,
		}
		if output.Path != nil {
			row.Path = new(normalize(*output.Path))
		}
		observed.Outputs = append(observed.Outputs, row)
	}
	assertNormalizedJSON(t, call.NativeResult, observed)
	preview := actual.Preview("parity-planner")
	for index := range preview.Outputs {
		preview.Outputs[index].Input = normalize(preview.Outputs[index].Input)
		if preview.Outputs[index].Output != nil {
			preview.Outputs[index].Output = new(normalize(*preview.Outputs[index].Output))
		}
	}
	assertNormalizedJSON(t, call.NativePreview, preview)
	require.Equal(t, call.Filesystem, discoverySnapshot(t, fixture))
	return actual
}

func TestOutputPlanningParity(t *testing.T) {
	for _, row := range loadOutputPlanningOracle(t).Cases {
		t.Run(inputPlanningSubtest(row.SourceCase), func(t *testing.T) {
			for index, call := range row.Calls {
				t.Run(fmt.Sprintf("call-%d", index+1), func(t *testing.T) {
					executeOutputPlanning(t, call)
				})
			}
		})
	}
}

func TestOutputPlanningControlsAreNotRecordings(t *testing.T) {
	loadOutputPlanningOracle(t)
	body, err := os.ReadFile("../../testdata/parity/output_planning.json")
	require.NoError(t, err)
	require.Error(t, recordingHygiene(body), "public synthetic controls must remain rejected as recordings")
	for _, recording := range loadParityInventory(t).Recordings {
		require.NotEqual(t, "testdata/parity/output_planning.json", recording.File)
	}
}
