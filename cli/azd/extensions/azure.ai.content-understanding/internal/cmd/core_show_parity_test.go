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
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

var coreShowNames = []string{"show-forms", "show-typed", "show-shared-forms", "show-conflict", "show-missing"}

type coreShowInput struct {
	Positional *string `json:"positional_analyzer_name"`
	Named      *string `json:"analyzer_name"`
}

type coreShowSourceCall struct {
	Operation  string          `json:"operation"`
	Parsed     coreShowInput   `json:"parsed"`
	Result     json.RawMessage `json:"result"`
	ResultType string          `json:"result_type"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type coreShowCase struct {
	Name            string                 `json:"name"`
	Source          string                 `json:"source"`
	Calls           []coreShowSourceCall   `json:"calls"`
	NativeError     *normalizedParityError `json:"native_error"`
	NativeErrorCode string                 `json:"native_error_code"`
}

type coreShowOracle struct {
	SchemaVersion int `json:"schema_version"`
	Source        struct {
		Repository               string            `json:"repository"`
		Commit                   string            `json:"commit"`
		License                  string            `json:"license"`
		Python                   string            `json:"python"`
		Files                    map[string]string `json:"files"`
		CommandPath              []string          `json:"command_path"`
		PositionalClassification string            `json:"positional_classification"`
	} `json:"source"`
	NativeResponse json.RawMessage `json:"native_response"`
	Boundaries     []string        `json:"boundaries"`
	Cases          []coreShowCase  `json:"cases"`
}

func loadCoreShowOracle(t *testing.T) coreShowOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/core_show_bindings.json", inventory.CoreShowBindings.File)
	body, err := os.ReadFile("../../testdata/parity/core_show_bindings.json")
	require.NoError(t, err)
	require.Equal(t, inventory.CoreShowBindings.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle coreShowOracle
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
	require.Equal(t, []string{"analyzer", "show"}, oracle.Source.CommandPath)
	require.Equal(t, "standalone-shortcut", oracle.Source.PositionalClassification)
	require.Len(t, oracle.Boundaries, 3)
	require.Len(t, oracle.Cases, len(coreShowNames))
	for index, row := range oracle.Cases {
		require.Equal(t, coreShowNames[index], row.Name)
		require.Contains(t, row.Source, "packages/core/tests/test_command_spec.py::test_")
		require.Len(t, row.Calls, []int{2, 1, 2, 1, 1}[index])
		require.Equal(t, index >= 3, row.NativeError != nil)
		require.Equal(t, index >= 3, row.NativeErrorCode != "")
		for _, call := range row.Calls {
			require.True(t, call.Operation == "bind" || call.Operation == "build_request")
			require.Equal(t, row.NativeError != nil, call.Error != nil)
			if call.Error != nil {
				require.Equal(t, "cu_cli_core.command_spec.CommandBindingError", call.Error.Type)
				require.NotEmpty(t, call.Error.Message)
				require.Equal(t, "null", string(call.Result))
				require.Empty(t, call.ResultType)
			} else if call.Operation == "build_request" {
				require.Equal(t, "cu_cli_core.contracts.AnalyzerShowRequest", call.ResultType)
			} else {
				require.Equal(t, "builtins.dict", call.ResultType)
			}
		}
	}
	return oracle
}

type coreShowObserver struct {
	cuclient.Client
	request json.RawMessage
	calls   []normalizedBackendCall
}

func (o *coreShowObserver) GetAnalyzer(ctx context.Context, name string) (cuclient.Analyzer, error) {
	request, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return cuclient.Analyzer{}, err
	}
	o.request = request
	result, err := o.Client.GetAnalyzer(ctx, name)
	if err != nil {
		return cuclient.Analyzer{}, err
	}
	raw, err := analyzerJSON(result)
	if err != nil {
		return cuclient.Analyzer{}, err
	}
	o.calls = append(o.calls, normalizedBackendCall{"get", []string{name}, raw})
	return result, nil
}

func coreAnalyzerForms(action string, input coreShowInput) [][]string {
	base := []string{"analyzer", action}
	if input.Positional != nil {
		base = append(base, *input.Positional)
	}
	if input.Named == nil {
		return [][]string{base}
	}
	var forms [][]string
	for _, flag := range []string{"--name", "-n", "-a"} {
		forms = append(forms, append(slices.Clone(base), flag, *input.Named))
	}
	return forms
}

func TestCoreShowBindingParity(t *testing.T) {
	oracle := loadCoreShowOracle(t)
	executions := 0
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			for index, call := range row.Calls {
				for variant, args := range coreAnalyzerForms("show", call.Parsed) {
					for _, format := range []string{"default", "json", "none"} {
						t.Run(fmt.Sprintf("%d/%d/%s", index, variant, format), func(t *testing.T) {
							executeCoreShowBinding(t, oracle.NativeResponse, row, call, args, format)
							executions++
						})
					}
				}
			}
		})
	}
	require.Equal(t, 45, executions)
}

func executeCoreShowBinding(
	t *testing.T, response json.RawMessage, row coreShowCase, call coreShowSourceCall, args []string, format string,
) {
	t.Helper()
	_ = newDefaultsFixture(t)
	expectedCalls := []normalizedBackendCall{}
	if row.NativeError == nil {
		var request struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal(call.Result, &request))
		expectedCalls = append(expectedCalls, normalizedBackendCall{"get", []string{request.Name}, response})
	}
	transport := &recordedTransport{fixture: normalizedHTTPFixture(t, expectedCalls)}
	credential := &recordedCredential{}
	observer := &coreShowObserver{request: json.RawMessage("null"), calls: []normalizedBackendCall{}}
	opens, closes, prompts := 0, 0, 0
	root := newRootCommand(func(
		_ *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		opens++
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
				return false, errors.New("Unexpected core-binding prompt.")
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	invocation := append(slices.Clone(args), "-o", format, "--no-prompt",
		"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion)
	root.SetArgs(invocation)
	err := root.ExecuteContext(t.Context())
	require.Empty(t, stderr.String())
	require.Zero(t, prompts)
	require.NoError(t, transport.complete())
	require.Equal(t, len(expectedCalls), credential.calls)
	assertNormalizedJSON(t, expectedCalls, observer.calls)
	if row.NativeError != nil {
		require.Error(t, err)
		local, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Equal(t, row.NativeErrorCode, local.Code)
		actual := normalizedParityError{
			Category: string(local.Category), Message: local.Message, Hint: &local.Suggestion,
		}
		assertNormalizedJSON(t, row.NativeError, actual)
		require.Equal(t, "null", string(observer.request))
		require.Zero(t, opens)
		require.Zero(t, closes)
		require.Empty(t, stdout.String())
		return
	}
	require.NoError(t, err)
	require.Equal(t, 1, opens)
	require.Equal(t, 1, closes)
	assertNormalizedJSON(t, call.Result, observer.request)
	if format == "none" {
		require.Empty(t, stdout.String())
	} else {
		assertNormalizedJSON(t, response, json.RawMessage(stdout.Bytes()))
	}
}
