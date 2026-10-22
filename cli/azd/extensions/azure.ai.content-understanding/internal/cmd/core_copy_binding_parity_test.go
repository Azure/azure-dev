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

	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type coreCopyInput struct {
	NamedSource           *string `json:"named_source"`
	NamedDestination      *string `json:"named_destination"`
	PositionalSource      *string `json:"positional_source"`
	PositionalDestination *string `json:"positional_destination"`
}

type coreCopyCall struct {
	Operation  string          `json:"operation"`
	Parsed     coreCopyInput   `json:"parsed"`
	Result     json.RawMessage `json:"result"`
	ResultType string          `json:"result_type"`
	Error      json.RawMessage `json:"error"`
}

type coreCopyGet struct {
	Name    string          `json:"name"`
	Result  json.RawMessage `json:"result"`
	Missing bool            `json:"missing"`
}

type coreCopyCase struct {
	Name      string                 `json:"name"`
	HTTP      []recordedInteraction  `json:"http"`
	Gets      []coreCopyGet          `json:"gets"`
	Copied    bool                   `json:"copied"`
	Result    json.RawMessage        `json:"result"`
	ErrorCode string                 `json:"error_code"`
	Error     *normalizedParityError `json:"error"`
}

type coreCopyOracle struct {
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
		OperandKind              string            `json:"operand_kind"`
	} `json:"source"`
	Calls       []coreCopyCall `json:"calls"`
	NativeCases []coreCopyCase `json:"native_cases"`
	Boundaries  []string       `json:"boundaries"`
}

func loadCoreCopyOracle(t *testing.T) coreCopyOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/core_copy_binding.json", inventory.CoreCopyBinding.File)
	body, err := os.ReadFile("../../testdata/parity/core_copy_binding.json")
	require.NoError(t, err)
	require.Equal(t, inventory.CoreCopyBinding.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle coreCopyOracle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&oracle))
	require.Equal(t, 1, oracle.SchemaVersion)
	require.Equal(t, inventory.Source.Repository, oracle.Source.Repository)
	require.Equal(t, paritySourceCommit, oracle.Source.Commit)
	require.Equal(t, "MIT", oracle.Source.License)
	require.NotEmpty(t, oracle.Source.Python)
	require.Len(t, oracle.Source.Files, 5)
	for path, digest := range oracle.Source.Files {
		require.Regexp(t, "^packages/core/.*\\.py$", path)
		require.NotContains(t, path, "..")
		require.Regexp(t, "^[0-9a-f]{64}$", digest)
	}
	require.Contains(t, oracle.Source.Files, "packages/core/src/cu_cli_core/operations/analyzer_copy.py")
	require.Equal(t, "packages/core/tests/test_command_spec.py::"+
		"test_canonical_and_standalone_shortcut_forms_bind_identically"+
		"[path4-canonical4-shortcut4-expected4]", oracle.SourceCase)
	require.Equal(t, []string{"analyzer", "copy"}, oracle.Source.CommandPath)
	require.Equal(t, "standalone-shortcut", oracle.Source.PositionalClassification)
	require.Equal(t, "analyzer-ids-not-profiles", oracle.Source.OperandKind)
	require.Len(t, oracle.Boundaries, 3)
	require.Len(t, oracle.Calls, 2)
	for _, call := range oracle.Calls {
		require.Equal(t, "bind", call.Operation)
		require.Equal(t, "builtins.dict", call.ResultType)
		require.Equal(t, "null", string(call.Error))
		assertNormalizedJSON(t, json.RawMessage(`{"source":"dev","destination":"prod"}`), call.Result)
	}
	canonical, positional := oracle.Calls[0].Parsed, oracle.Calls[1].Parsed
	source, destination := "dev", "prod"
	require.Equal(t, coreCopyInput{NamedSource: &source, NamedDestination: &destination}, canonical)
	require.Equal(t, coreCopyInput{
		PositionalSource: &source, PositionalDestination: &destination,
	}, positional)
	names := []string{"same-resource", "destination-exists", "source-missing"}
	require.Len(t, oracle.NativeCases, len(names))
	for index, row := range oracle.NativeCases {
		require.Equal(t, names[index], row.Name)
		require.Equal(t, index == 0, row.Copied)
		require.Equal(t, index != 0, row.Error != nil)
		require.Equal(t, index != 0, row.ErrorCode != "")
		require.Len(t, row.HTTP, 3-index)
	}
	return oracle
}

type coreCopyObserver struct {
	cuclient.Client
	gets     []coreCopyGet
	requests []cuclient.CopyAnalyzerRequest
	results  []json.RawMessage
	grants   int
}

func (o *coreCopyObserver) GetAnalyzer(ctx context.Context, name string) (cuclient.Analyzer, error) {
	value, err := o.Client.GetAnalyzer(ctx, name)
	call := coreCopyGet{Name: name, Result: json.RawMessage("null")}
	if err == nil {
		call.Result, err = analyzerJSON(value)
	} else if domain, ok := errors.AsType[*cuclient.Error](err); ok {
		call.Missing = domain.Kind == cuclient.ErrorNotFound && domain.StatusCode == 404
	}
	o.gets = append(o.gets, call)
	return value, err
}

func (o *coreCopyObserver) CopyAnalyzer(
	ctx context.Context, request cuclient.CopyAnalyzerRequest,
) (cuclient.AnalyzerResponse, error) {
	o.requests = append(o.requests, request)
	response, err := o.Client.CopyAnalyzer(ctx, request)
	if err != nil {
		return response, err
	}
	raw, err := analyzerJSON(response.Analyzer)
	if err != nil {
		return cuclient.AnalyzerResponse{}, err
	}
	o.results = append(o.results, raw)
	return response, nil
}

func (o *coreCopyObserver) GrantCopyAuthorization(
	context.Context, cuclient.GrantCopyAuthorizationRequest,
) (cuclient.CopyAuthorization, error) {
	o.grants++
	return cuclient.CopyAuthorization{}, errors.New("Same-resource binding attempted a cross-resource copy grant.")
}

func TestCoreCopyBindingParity(t *testing.T) {
	oracle := loadCoreCopyOracle(t)
	executions := 0
	for _, row := range oracle.NativeCases {
		for index, call := range oracle.Calls {
			args := []string{"analyzer", "copy"}
			if call.Parsed.NamedSource != nil {
				args = append(args, "--source", *call.Parsed.NamedSource,
					"--destination", *call.Parsed.NamedDestination)
			} else {
				args = append(args, *call.Parsed.PositionalSource, *call.Parsed.PositionalDestination)
			}
			for _, format := range []string{"default", "json", "none"} {
				t.Run(fmt.Sprintf("%s/%d/%s", row.Name, index, format), func(t *testing.T) {
					executeCoreCopyBinding(t, call, row, args, format)
					executions++
				})
			}
		}
	}
	require.Equal(t, 18, executions)
}

func executeCoreCopyBinding(
	t *testing.T, call coreCopyCall, row coreCopyCase, args []string, format string,
) {
	t.Helper()
	_ = newDefaultsFixture(t)
	transport := &recordedTransport{fixture: recordedFixture{Interactions: row.HTTP}}
	credential := &recordedCredential{}
	observer := &coreCopyObserver{}
	opens, closes, prompts, discoveries, switches := 0, 0, 0, 0, 0
	var binding map[string]string
	root := newRootCommand(func(
		cmd *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		opens++
		binding = map[string]string{}
		if call.Parsed.NamedSource != nil {
			require.Empty(t, cmd.Flags().Args())
			for _, flag := range []string{"source", "destination"} {
				require.True(t, cmd.Flags().Changed(flag))
				value, err := cmd.Flags().GetString(flag)
				require.NoError(t, err)
				binding[flag] = value
			}
		} else {
			require.False(t, cmd.Flags().Changed("source"))
			require.False(t, cmd.Flags().Changed("destination"))
			require.Len(t, cmd.Flags().Args(), 2)
			binding["source"], binding["destination"] = cmd.Flags().Args()[0], cmd.Flags().Args()[1]
		}
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
				return false, errors.New("Unexpected same-resource copy binding prompt.")
			},
			resolveAccount: func(context.Context, string, string, string) (armclient.Account, error) {
				discoveries++
				return armclient.Account{}, errors.New("Analyzer IDs were treated as resource selectors.")
			},
			clientForEndpoint: func(string) (cuclient.Client, error) {
				switches++
				return nil, errors.New("Same-resource copy switched clients.")
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append(slices.Clone(args), "-o", format, "--no-prompt",
		"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion))
	runErr := root.ExecuteContext(t.Context())
	require.NoError(t, transport.complete())
	require.Empty(t, stderr.String())
	require.Equal(t, 1, opens)
	require.Equal(t, 1, closes)
	require.Equal(t, len(row.HTTP), credential.calls)
	require.Zero(t, prompts)
	require.Zero(t, discoveries)
	require.Zero(t, switches)
	require.Zero(t, observer.grants)
	assertNormalizedJSON(t, call.Result, binding)
	assertNormalizedJSON(t, row.Gets, observer.gets)
	if row.Copied {
		require.NoError(t, runErr)
		require.Len(t, observer.requests, 1)
		require.Equal(t, cuclient.CopyAnalyzerRequest{
			SourceAnalyzerID: binding["source"], DestinationAnalyzerID: binding["destination"],
		}, observer.requests[0])
		require.Len(t, observer.results, 1)
		assertNormalizedJSON(t, row.Result, observer.results[0])
		if format == "none" {
			require.Empty(t, stdout.String())
		} else {
			assertNormalizedJSON(t, row.Result, json.RawMessage(stdout.Bytes()))
		}
	} else {
		require.Error(t, runErr)
		service, ok := errors.AsType[*azdext.ServiceError](runErr)
		require.True(t, ok)
		require.Equal(t, row.ErrorCode, service.ErrorCode)
		assertNormalizedJSON(t, row.Error, normalizedParityError{
			Category: "service", Message: service.Message, Hint: &service.Suggestion, StatusCode: &service.StatusCode,
		})
		require.Empty(t, observer.requests)
		require.Empty(t, observer.results)
		require.Empty(t, stdout.String())
	}
}
