// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/schema"

	"github.com/stretchr/testify/require"
)

type schemaValidationCall struct {
	Operation string `json:"operation"`
	Input     struct {
		Args   []json.RawMessage `json:"args"`
		Kwargs struct {
			APIVersion *string `json:"api_version"`
		} `json:"kwargs"`
	} `json:"input"`
	Result                json.RawMessage          `json:"result"`
	ResultType            string                   `json:"result_type"`
	ElementTypes          []string                 `json:"element_types"`
	Error                 json.RawMessage          `json:"error"`
	NativeInput           *string                  `json:"native_input"`
	NativeResult          *schema.ValidationResult `json:"native_result"`
	NativeDiagnosticPaths []string                 `json:"native_diagnostic_paths"`
}

type schemaValidationCase struct {
	SourceCase string                 `json:"source_case"`
	Calls      []schemaValidationCall `json:"calls"`
}

type schemaValidationOracle struct {
	SchemaVersion int    `json:"schema_version"`
	Role          string `json:"role"`
	Source        struct {
		Repository     string            `json:"repository"`
		Commit         string            `json:"commit"`
		License        string            `json:"license"`
		Python         string            `json:"python"`
		Platform       string            `json:"platform"`
		Files          map[string]string `json:"files"`
		CaseCount      int               `json:"case_count"`
		CallCount      int               `json:"call_count"`
		AllowedTypes   []string          `json:"allowed_types"`
		AllowedMethods []string          `json:"allowed_methods"`
	} `json:"source"`
	Boundaries []string `json:"boundaries"`
	TypeProbes []struct {
		Type     string                  `json:"type"`
		Body     json.RawMessage         `json:"body"`
		Expected schema.ValidationResult `json:"expected"`
	} `json:"type_probes"`
	Cases []schemaValidationCase `json:"cases"`
}

const schemaValidationGoTest = "internal/cmd/schema_validation_parity_test.go::TestSchemaValidationParity"

func loadSchemaValidationOracle(t *testing.T) schemaValidationOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/schema_validation.json", inventory.SchemaValidation.File)
	body, err := os.ReadFile("../../testdata/parity/schema_validation.json")
	require.NoError(t, err)
	require.Equal(t, inventory.SchemaValidation.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle schemaValidationOracle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&oracle))
	require.Equal(t, 1, oracle.SchemaVersion)
	require.Equal(t, "public-source-validator-controls-not-service-recordings", oracle.Role)
	require.Equal(t, inventory.Source.Repository, oracle.Source.Repository)
	require.Equal(t, paritySourceCommit, oracle.Source.Commit)
	require.Equal(t, "MIT", oracle.Source.License)
	require.Equal(t, "Linux", oracle.Source.Platform)
	require.NotEmpty(t, oracle.Source.Python)
	require.Len(t, oracle.Source.Files, 6)
	for path, digest := range oracle.Source.Files {
		require.True(t, strings.HasPrefix(path, "packages/core/"))
		require.NotContains(t, path, "..")
		require.Regexp(t, "^[0-9a-f]{64}$", digest)
	}
	require.Equal(t, 31, oracle.Source.CaseCount)
	require.Equal(t, 32, oracle.Source.CallCount)
	require.Len(t, oracle.Cases, 31)
	require.Len(t, oracle.Boundaries, 4)
	require.Equal(t, []string{"array", "boolean", "date", "integer", "json", "number", "object", "string", "time"},
		oracle.Source.AllowedTypes)
	require.Equal(t, []string{"classify", "extract", "generate"}, oracle.Source.AllowedMethods)
	require.Len(t, oracle.TypeProbes, 10)
	probeTypes := []string{}
	for _, probe := range oracle.TypeProbes {
		probeTypes = append(probeTypes, probe.Type)
		var definition struct {
			FieldSchema struct {
				Fields map[string]struct {
					Type string `json:"type"`
				} `json:"fields"`
			} `json:"fieldSchema"`
		}
		require.NoError(t, json.Unmarshal(probe.Body, &definition))
		require.Equal(t, probe.Type, definition.FieldSchema.Fields["vendor"].Type)
	}
	require.Equal(t, append(slices.Clone(oracle.Source.AllowedTypes), "datetime"), probeTypes)
	mapped := map[string]bool{}
	for _, row := range inventory.Cases {
		if slices.ContainsFunc(row.GoTests, func(ref string) bool {
			return strings.HasPrefix(ref, schemaValidationGoTest+"/")
		}) {
			require.Contains(t, []string{"evidenced", "approved"}, row.State)
			require.Equal(t, "difference", row.Disposition)
			require.NoError(t, parityState(row.State, row.Approval))
			require.Contains(t, row.GoTests, schemaValidationGoTest+"/"+sourceParitySubtest(row.Source))
			mapped[row.Source] = true
		}
	}
	require.Len(t, mapped, 31)
	calls := 0
	for _, row := range oracle.Cases {
		require.True(t, mapped[row.SourceCase])
		require.True(t, strings.HasPrefix(row.SourceCase, "packages/core/tests/test_schema_validation.py::"))
		delete(mapped, row.SourceCase)
		for _, call := range row.Calls {
			calls++
			require.Equal(t, "null", string(call.Error))
			require.Contains(t, []string{
				"validate_schema", "parse_and_validate", "schema_pinned_version", "allowed_types",
			}, call.Operation)
			require.NotEmpty(t, call.ResultType)
			if call.Operation != "allowed_types" {
				require.Len(t, call.Input.Args, 1)
				require.NotNil(t, call.NativeInput)
				if call.Operation == "parse_and_validate" {
					var text string
					require.NoError(t, json.Unmarshal(call.Input.Args[0], &text))
					require.Equal(t, text, *call.NativeInput)
				} else {
					assertNormalizedJSON(t, call.Input.Args[0], json.RawMessage(*call.NativeInput))
				}
			}
			if call.Operation == "parse_and_validate" {
				require.Equal(t, "builtins.tuple", call.ResultType)
				require.Equal(t, []string{"cu_cli_core.schema_validation.ValidationResult", "builtins.NoneType"},
					call.ElementTypes)
			}
		}
	}
	require.Empty(t, mapped)
	require.Equal(t, oracle.Source.CallCount, calls)
	return oracle
}

func assertStructuralSchema(
	t *testing.T, body []byte, version string, expected schema.ValidationResult,
) schema.ValidationResult {
	t.Helper()
	before := bytes.Clone(body)
	actual, err := schema.Validate(body, version, false)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.Equal(t, before, body, "native validator mutated its input")
	return actual
}

func TestSchemaValidationParity(t *testing.T) {
	oracle := loadSchemaValidationOracle(t)
	for _, row := range oracle.Cases {
		t.Run(sourceParitySubtest(row.SourceCase), func(t *testing.T) {
			for _, call := range row.Calls {
				switch call.Operation {
				case "allowed_types":
					assertNormalizedJSON(t, call.Result, oracle.Source.AllowedTypes)
					for _, probe := range oracle.TypeProbes {
						for _, version := range []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion} {
							assertStructuralSchema(t, probe.Body, version, probe.Expected)
						}
					}
				case "schema_pinned_version":
					require.NotNil(t, call.NativeInput)
					prepared, pinned, err := schema.PrepareDefinition([]byte(*call.NativeInput))
					require.NoError(t, err)
					require.Len(t, call.Input.Args, 1)
					assertNormalizedJSON(t, call.Input.Args[0], prepared)
					var actual *string
					if pinned != "" {
						actual = &pinned
					}
					assertNormalizedJSON(t, call.Result, actual)
				default:
					require.NotNil(t, call.NativeInput)
					require.NotNil(t, call.NativeResult)
					versions := []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion}
					if call.Input.Kwargs.APIVersion != nil {
						versions = []string{*call.Input.Kwargs.APIVersion}
					}
					for _, version := range versions {
						actual := assertStructuralSchema(t, []byte(*call.NativeInput), version, *call.NativeResult)
						paths := []string{}
						for _, findings := range [][]schema.Finding{actual.Errors, actual.Warnings} {
							for _, finding := range findings {
								paths = append(paths, finding.DiagnosticPath())
							}
						}
						require.Equal(t, call.NativeDiagnosticPaths, paths)
					}
				}
			}
		})
	}
}
