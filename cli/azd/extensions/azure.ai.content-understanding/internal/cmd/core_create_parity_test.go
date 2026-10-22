// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type coreCreateCall struct {
	Operation string `json:"operation"`
	Parsed    struct {
		coreShowInput
		SchemaPath string `json:"schema_path"`
	} `json:"parsed"`
	Result          json.RawMessage `json:"result"`
	ResultType      string          `json:"result_type"`
	Error           json.RawMessage `json:"error"`
	SchemaPathType  string          `json:"schema_path_type"`
	BoundSchemaType string          `json:"bound_schema_type"`
}

type coreCreateOracle struct {
	SchemaVersion int    `json:"schema_version"`
	SourceCase    string `json:"source_case"`
	Source        struct {
		Repository               string            `json:"repository"`
		Commit                   string            `json:"commit"`
		License                  string            `json:"license"`
		Python                   string            `json:"python"`
		Files                    map[string]string `json:"files"`
		CommandPath              []string          `json:"command_path"`
		PositionalClassification string            `json:"positional_classification"`
	} `json:"source"`
	Calls        []coreCreateCall `json:"calls"`
	NativeName   string           `json:"native_name"`
	NativeSchema json.RawMessage  `json:"native_schema"`
	NativeResult json.RawMessage  `json:"native_result"`
	Boundaries   []string         `json:"boundaries"`
}

func loadCoreCreateOracle(t *testing.T) coreCreateOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/core_create_binding.json", inventory.CoreCreateBinding.File)
	body, err := os.ReadFile("../../testdata/parity/core_create_binding.json")
	require.NoError(t, err)
	require.Equal(t, inventory.CoreCreateBinding.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle coreCreateOracle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&oracle))
	require.Equal(t, 1, oracle.SchemaVersion)
	require.Equal(t, inventory.Source.Repository, oracle.Source.Repository)
	require.Equal(t, paritySourceCommit, oracle.Source.Commit)
	require.Equal(t, "MIT", oracle.Source.License)
	require.NotEmpty(t, oracle.Source.Python)
	require.Len(t, oracle.Source.Files, 4)
	for path, digest := range oracle.Source.Files {
		require.Regexp(t, "^packages/core/.*\\.py$", path)
		require.NotContains(t, path, "..")
		require.Regexp(t, "^[0-9a-f]{64}$", digest)
	}
	require.Equal(t, "packages/core/tests/test_command_spec.py::"+
		"test_canonical_and_standalone_shortcut_forms_bind_identically"+
		"[path1-canonical1-shortcut1-expected1]", oracle.SourceCase)
	require.Equal(t, []string{"analyzer", "create"}, oracle.Source.CommandPath)
	require.Equal(t, "standalone-shortcut", oracle.Source.PositionalClassification)
	require.Equal(t, "invoice_v1", oracle.NativeName)
	require.Len(t, oracle.Boundaries, 3)
	require.Len(t, oracle.Calls, 2)
	for _, call := range oracle.Calls {
		require.Equal(t, "bind", call.Operation)
		require.Equal(t, "builtins.dict", call.ResultType)
		require.Equal(t, "pathlib.PosixPath", call.SchemaPathType)
		require.Equal(t, "pathlib.PosixPath", call.BoundSchemaType)
		require.Equal(t, "schema.json", call.Parsed.SchemaPath)
		require.Equal(t, "null", string(call.Error))
		assertNormalizedJSON(t, json.RawMessage(`{"name":"invoice-v1","schema":"schema.json"}`), call.Result)
	}
	require.Nil(t, oracle.Calls[0].Parsed.Positional)
	require.NotNil(t, oracle.Calls[0].Parsed.Named)
	require.Equal(t, "invoice-v1", *oracle.Calls[0].Parsed.Named)
	require.Nil(t, oracle.Calls[1].Parsed.Named)
	require.NotNil(t, oracle.Calls[1].Parsed.Positional)
	require.Equal(t, "invoice-v1", *oracle.Calls[1].Parsed.Positional)
	return oracle
}

type coreCreateObserver struct {
	cuclient.Client
	requests []cuclient.CreateAnalyzerRequest
	results  []json.RawMessage
}

func (o *coreCreateObserver) CreateAnalyzer(
	ctx context.Context, request cuclient.CreateAnalyzerRequest,
) (cuclient.AnalyzerResponse, error) {
	o.requests = append(o.requests, request)
	result, err := o.Client.CreateAnalyzer(ctx, request)
	if err != nil {
		return result, err
	}
	raw, err := analyzerJSON(result.Analyzer)
	if err != nil {
		return cuclient.AnalyzerResponse{}, err
	}
	o.results = append(o.results, raw)
	return result, nil
}

func TestCoreCreateBindingParity(t *testing.T) {
	oracle := loadCoreCreateOracle(t)
	executions := 0
	for index, call := range oracle.Calls {
		input := call.Parsed.coreShowInput
		if input.Positional != nil {
			input.Positional = &oracle.NativeName
		}
		if input.Named != nil {
			input.Named = &oracle.NativeName
		}
		for variant, args := range coreAnalyzerForms("create", input) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(fmt.Sprintf("%d/%d/%s", index, variant, format), func(t *testing.T) {
					executeCoreCreateBinding(t, oracle, call, args, format)
					executions++
				})
			}
		}
	}
	require.Equal(t, 12, executions)
}

func executeCoreCreateBinding(
	t *testing.T, oracle coreCreateOracle, call coreCreateCall, args []string, format string,
) {
	t.Helper()
	_ = newDefaultsFixture(t)
	directory := filepath.Join(t.TempDir(), "schema folder with spaces")
	require.NoError(t, os.Mkdir(directory, 0o700))
	schemaPath := filepath.Join(directory, call.Parsed.SchemaPath)
	require.NoError(t, os.WriteFile(schemaPath, oracle.NativeSchema, 0o600))
	var interaction recordedInteraction
	interaction.Request.Method = "PUT"
	interaction.Request.URL = "https://sanitized.services.ai.azure.com/contentunderstanding/analyzers/" +
		oracle.NativeName + "?api-version=" + cuclient.DefaultAPIVersion + "&allowReplace=false"
	interaction.Request.Headers = map[string]string{"Content-Type": "application/json", "Accept": "application/json"}
	interaction.Request.BodyMode = "json"
	interaction.Request.Body = oracle.NativeSchema
	interaction.Response.Status = 201
	interaction.Response.Headers = map[string]string{"Content-Type": "application/json"}
	interaction.Response.Body = oracle.NativeResult
	transport := &recordedTransport{fixture: recordedFixture{Interactions: []recordedInteraction{interaction}}}
	credential := &recordedCredential{}
	observer := &coreCreateObserver{}
	opens, closes, prompts := 0, 0, 0
	var parsedSchemaPath string
	root := newRootCommand(func(
		cmd *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		opens++
		var err error
		parsedSchemaPath, err = cmd.Flags().GetString("schema")
		require.NoError(t, err)
		require.Equal(t, schemaPath, parsedSchemaPath)
		require.True(t, filepath.IsAbs(parsedSchemaPath))
		require.Equal(t, "https://sanitized.services.ai.azure.com", options.Endpoint)
		require.Equal(t, cuclient.DefaultAPIVersion, options.APIVersion)
		client, err := cuclient.NewSDKTransport(options.Endpoint, credential,
			&cuclient.TransportOptions{APIVersion: options.APIVersion, Transport: transport})
		if err != nil {
			return nil, err
		}
		observer.Client = client
		return &serviceSession{
			client: observer, apiVersion: options.APIVersion, close: func() { closes++ },
			confirm: func(context.Context, string) (bool, error) {
				prompts++
				return false, errors.New("Unexpected shared-create binding prompt.")
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	schemaFlag := "--schema"
	if format == "json" {
		schemaFlag = "-s"
	}
	root.SetArgs(append(slices.Clone(args), schemaFlag, schemaPath, "-o", format, "--no-prompt",
		"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion))
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Empty(t, stderr.String())
	require.Zero(t, prompts)
	require.Equal(t, 1, opens)
	require.Equal(t, 1, closes)
	require.Equal(t, 1, credential.calls)
	require.NoError(t, transport.complete())
	require.Len(t, observer.requests, 1)
	require.Len(t, observer.results, 1)
	require.Equal(t, oracle.NativeName, observer.requests[0].AnalyzerID)
	assertNormalizedJSON(t, oracle.NativeSchema, observer.requests[0].Definition)
	assertNormalizedJSON(t, oracle.NativeResult, observer.results[0])
	var adaptedBinding map[string]string
	require.NoError(t, json.Unmarshal(call.Result, &adaptedBinding))
	adaptedBinding["name"] = oracle.NativeName
	relative, err := filepath.Rel(directory, parsedSchemaPath)
	require.NoError(t, err)
	require.Equal(t, call.Parsed.SchemaPath, relative)
	assertNormalizedJSON(t, adaptedBinding, map[string]string{
		"name": observer.requests[0].AnalyzerID, "schema": relative,
	})
	if format == "none" {
		require.Empty(t, stdout.String())
	} else {
		assertNormalizedJSON(t, oracle.NativeResult, json.RawMessage(stdout.Bytes()))
	}
}

func TestCoreCreateSourceNameRejected(t *testing.T) {
	oracle := loadCoreCreateOracle(t)
	executions := 0
	for index, call := range oracle.Calls {
		for variant, args := range coreAnalyzerForms("create", call.Parsed.coreShowInput) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(fmt.Sprintf("%d/%d/%s", index, variant, format), func(t *testing.T) {
					_ = newDefaultsFixture(t)
					opens := 0
					root := newRootCommand(func(
						*cobra.Command, *azdext.ExtensionContext, config.Options,
					) (*serviceSession, error) {
						opens++
						return nil, errors.New("Invalid source create ID opened a service session.")
					})
					var stdout, stderr bytes.Buffer
					root.SetOut(&stdout)
					root.SetErr(&stderr)
					schemaPath := filepath.Join(t.TempDir(), call.Parsed.SchemaPath)
					root.SetArgs(append(slices.Clone(args), "--schema", schemaPath, "-o", format, "--no-prompt"))
					err := root.ExecuteContext(t.Context())
					require.Error(t, err)
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					require.Equal(t, "invalid_analyzer_schema", local.Code)
					require.Equal(t, "Invalid analyzer schema at analyzerId: "+
						"Custom analyzer IDs require 1-64 ASCII letters, numbers, or underscores.", local.Message)
					require.Zero(t, opens)
					require.NoFileExists(t, schemaPath)
					require.Empty(t, stdout.String())
					require.Empty(t, stderr.String())
					executions++
				})
			}
		}
	}
	require.Equal(t, 12, executions)
}
