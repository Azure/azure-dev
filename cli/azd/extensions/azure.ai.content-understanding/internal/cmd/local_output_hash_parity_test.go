// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/analysis"

	"github.com/stretchr/testify/require"
)

type localHashBinding struct {
	ID             int    `json:"id"`
	Reference      string `json:"reference"`
	SourceSHA256   string `json:"source_sha256"`
	ObservedSHA256 string `json:"observed_sha256"`
}

type localHashAdaptation struct {
	SourcePath             string `json:"source_path"`
	SourceRelative         string `json:"source_relative"`
	NativePathTemplate     string `json:"native_path_template"`
	NativeRelativeTemplate string `json:"native_relative_template"`
}

type localHashCall struct {
	outputPlanningCall
	HashMode            string                `json:"hash_mode"`
	Bindings            []localHashBinding    `json:"bindings"`
	Adaptations         []localHashAdaptation `json:"path_adaptations"`
	SourceDigestLengths []int                 `json:"source_digest_lengths"`
}

type localHashCase struct {
	SourceCase          string          `json:"source_case"`
	CaptureTempRelative string          `json:"capture_temp_relative"`
	Calls               []localHashCall `json:"calls"`
}

type localHashOracle struct {
	SchemaVersion int    `json:"schema_version"`
	Role          string `json:"role"`
	Source        struct {
		Repository  string            `json:"repository"`
		Commit      string            `json:"commit"`
		License     string            `json:"license"`
		Python      string            `json:"python"`
		Platform    string            `json:"platform"`
		Files       map[string]string `json:"files"`
		Functions   []string          `json:"functions"`
		CaseCount   int               `json:"case_count"`
		CallCount   int               `json:"call_count"`
		CaptureBase string            `json:"capture_base"`
	} `json:"source"`
	Boundaries []string        `json:"boundaries"`
	Cases      []localHashCase `json:"cases"`
}

const localHashGoTest = "internal/cmd/local_output_hash_parity_test.go::TestLocalOutputHashParity"

func loadLocalHashOracle(t *testing.T) localHashOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/local_output_hashes.json", inventory.LocalOutputHashes.File)
	body, err := os.ReadFile("../../testdata/parity/local_output_hashes.json")
	require.NoError(t, err)
	require.Equal(t, inventory.LocalOutputHashes.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle localHashOracle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&oracle))
	require.Equal(t, 1, oracle.SchemaVersion)
	require.Equal(t, "public-source-filesystem-controls-not-service-recordings", oracle.Role)
	require.Equal(t, inventory.Source.Repository, oracle.Source.Repository)
	require.Equal(t, paritySourceCommit, oracle.Source.Commit)
	require.Equal(t, "MIT", oracle.Source.License)
	require.Equal(t, "Linux", oracle.Source.Platform)
	require.NotEmpty(t, oracle.Source.Python)
	require.Equal(t, "source-local-output-hash-controls", oracle.Source.CaptureBase)
	require.Equal(t, 5, oracle.Source.CaseCount)
	require.Equal(t, 7, oracle.Source.CallCount)
	require.Len(t, oracle.Cases, 5)
	require.Len(t, oracle.Source.Functions, 3)
	require.Len(t, oracle.Source.Files, 5)
	for path, digest := range loadOutputPlanningOracle(t).Source.Files {
		require.Equal(t, digest, oracle.Source.Files[path])
	}
	require.Len(t, oracle.Boundaries, 4)
	mapped := map[string]bool{}
	for _, row := range inventory.Cases {
		if slices.ContainsFunc(row.GoTests, func(ref string) bool { return strings.HasPrefix(ref, localHashGoTest+"/") }) {
			require.Contains(t, []string{"evidenced", "approved"}, row.State)
			require.Equal(t, "difference", row.Disposition)
			require.NoError(t, parityState(row.State, row.Approval))
			require.Contains(t, row.GoTests, localHashGoTest+"/"+inputPlanningSubtest(row.Source))
			mapped[row.Source] = true
		}
	}
	require.Len(t, mapped, 5)
	calls := 0
	for _, row := range oracle.Cases {
		require.True(t, mapped[row.SourceCase])
		delete(mapped, row.SourceCase)
		require.True(t, filepath.IsLocal(row.CaptureTempRelative))
		for _, call := range row.Calls {
			calls++
			require.Len(t, call.Input.Args, 1)
			require.Len(t, call.Bindings, 2)
			require.Equal(t, "plan_outputs", call.Operation)
			require.Contains(t, []string{"original-sha256", "source-only-forced-digest"}, call.HashMode)
			for index, binding := range call.Bindings {
				require.Equal(t, index, binding.ID)
				require.Regexp(t, "^[0-9a-f]{64}$", binding.SourceSHA256)
				if call.HashMode == "original-sha256" {
					require.Equal(t, binding.SourceSHA256, binding.ObservedSHA256)
				} else {
					require.Equal(t, sha256Text([]byte("collision")), binding.ObservedSHA256)
				}
			}
			if call.NativeError != nil {
				require.Equal(t, "output_collision", call.NativeError.Code)
				require.Len(t, call.Adaptations, 8)
				require.Equal(t, "null", string(call.Result))
				require.NotEqual(t, "null", string(call.Error))
				for index, adaptation := range call.Adaptations {
					length := (index + 1) * 8
					binding := call.Bindings[0]
					require.Equal(t, binding.Reference+"."+binding.SourceSHA256[:length], adaptation.SourcePath)
					require.Equal(t, filepath.Base(adaptation.SourcePath), adaptation.SourceRelative)
					require.Equal(t, binding.Reference+fmt.Sprintf(".$SHA0_%d", length), adaptation.NativePathTemplate)
					require.Equal(t, filepath.Base(adaptation.NativePathTemplate), adaptation.NativeRelativeTemplate)
				}
			} else {
				require.Empty(t, call.Adaptations)
				require.Equal(t, "cu_cli_core.contracts.ExecutionPlan", call.ResultType)
				lengths := slices.Clone(call.SourceDigestLengths)
				slices.Sort(lengths)
				expected := []int{8, 8}
				if call.HashMode == "source-only-forced-digest" {
					expected = []int{8, 16}
				}
				require.Equal(t, expected, lengths)
			}
		}
	}
	require.Empty(t, mapped)
	require.Equal(t, oracle.Source.CallCount, calls)
	return oracle
}

func adaptLocalHashCall(t *testing.T, root string, source localHashCall) outputPlanningCall {
	t.Helper()
	body, err := json.Marshal(source.outputPlanningCall)
	require.NoError(t, err)
	var call outputPlanningCall
	require.NoError(t, json.Unmarshal(body, &call))
	resolve := func(value string) string {
		for _, binding := range source.Bindings {
			digest := sha256Text([]byte(expandPlanningPath(t, root, binding.Reference)))
			for length := 8; length <= 64; length += 8 {
				value = strings.ReplaceAll(value, fmt.Sprintf("$SHA%d_%d", binding.ID, length), digest[:length])
			}
		}
		require.NotContains(t, value, "$SHA")
		return value
	}
	for _, adaptation := range source.Adaptations {
		files, inputs := 0, 0
		for index := range call.Filesystem {
			entry := &call.Filesystem[index]
			if "$TMP/"+entry.Path == adaptation.SourcePath {
				require.Equal(t, "file", entry.Kind)
				entry.Path = strings.TrimPrefix(resolve(adaptation.NativePathTemplate), "$TMP/")
				files++
			}
		}
		for index := range call.Input.Args[0].Inputs {
			input := &call.Input.Args[0].Inputs[index]
			if input.Path != nil && *input.Path == adaptation.SourcePath {
				require.Equal(t, adaptation.SourceRelative, input.RelativePath)
				input.Path = new(resolve(adaptation.NativePathTemplate))
				input.RelativePath = resolve(adaptation.NativeRelativeTemplate)
				inputs++
			}
		}
		require.Equal(t, 1, files)
		require.Equal(t, 1, inputs)
	}
	call.NativeResult = json.RawMessage(resolve(string(call.NativeResult)))
	call.NativePreview = json.RawMessage(resolve(string(call.NativePreview)))
	return call
}

func localHashPaths(t *testing.T, root string, plan analysis.Plan) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, output := range plan.Outputs {
		require.NotNil(t, output.Path)
		reference := strings.ReplaceAll(filepath.ToSlash(output.Input.Reference), filepath.ToSlash(root), "$TMP")
		result[reference] = strings.ReplaceAll(filepath.ToSlash(*output.Path), filepath.ToSlash(root), "$TMP")
	}
	return result
}

func probeLocalHashReservation(t *testing.T, source localHashCall, reserved int) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	call := adaptLocalHashCall(t, root, source)
	call.Input.Args[0].Inputs = call.Input.Args[0].Inputs[:2+reserved]
	call.NativeError = nil
	suffix := ".result.json"
	if call.Input.Kwargs.View == "llm-input" {
		suffix = ".result.md"
	}
	expected := outputPlanningResult{Existing: "error", LLMInput: call.Input.Kwargs.View == "llm-input"}
	preview := analysis.DryRun{DryRun: true, Analyzer: "parity-planner"}
	for index, input := range call.Input.Args[0].Inputs {
		require.NotNil(t, input.Path)
		name := input.RelativePath
		if index < 2 {
			length := 8
			if index == 0 {
				length *= reserved + 1
			}
			digest := sha256Text([]byte(expandPlanningPath(t, root, *input.Path)))
			name += "." + digest[:length]
		}
		path := *call.Input.Kwargs.OutputDir + "/" + name + suffix
		expected.Outputs = append(expected.Outputs, outputPlanningOutput{
			Input: outputPlanningInput{Path: *input.Path, Reference: *input.Path, Relative: input.RelativePath},
			Path:  new(path),
		})
		preview.Outputs = append(preview.Outputs, analysis.DryRunOutput{Input: *input.Path, Output: new(path)})
	}
	call.NativeResult, err = json.Marshal(expected)
	require.NoError(t, err)
	call.NativePreview, err = json.Marshal(preview)
	require.NoError(t, err)
	executeOutputPlanningAt(t, root, call)
}

func TestLocalOutputHashParity(t *testing.T) {
	for _, row := range loadLocalHashOracle(t).Cases {
		t.Run(inputPlanningSubtest(row.SourceCase), func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			var previous map[string]string
			for index, source := range row.Calls {
				t.Run(fmt.Sprintf("source-call-%d", index+1), func(t *testing.T) {
					before, err := json.Marshal(source)
					require.NoError(t, err)
					call := adaptLocalHashCall(t, root, source)
					actual := executeOutputPlanningAt(t, root, call)
					if source.NativeError == nil {
						paths := localHashPaths(t, root, actual)
						if previous != nil {
							require.Equal(t, previous, paths)
						}
						previous = paths
					}
					after, err := json.Marshal(source)
					require.NoError(t, err)
					require.Equal(t, before, after, "native adaptation rewrote source evidence")
				})
			}
			if row.Calls[0].NativeError != nil {
				for reserved := range 8 {
					t.Run(fmt.Sprintf("native-only-reserved-%d", reserved), func(t *testing.T) {
						probeLocalHashReservation(t, row.Calls[0], reserved)
					})
				}
			}
		})
	}
}
