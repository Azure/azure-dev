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

type coreDeleteOracle struct {
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
	Calls          []coreShowSourceCall `json:"calls"`
	NativeAnalyzer json.RawMessage      `json:"native_analyzer"`
	NativeResult   json.RawMessage      `json:"native_result"`
	Boundaries     []string             `json:"boundaries"`
}

func loadCoreDeleteOracle(t *testing.T) coreDeleteOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/core_delete_binding.json", inventory.CoreDeleteBinding.File)
	body, err := os.ReadFile("../../testdata/parity/core_delete_binding.json")
	require.NoError(t, err)
	require.Equal(t, inventory.CoreDeleteBinding.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle coreDeleteOracle
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
		"[path2-canonical2-shortcut2-expected2]", oracle.SourceCase)
	require.Equal(t, []string{"analyzer", "delete"}, oracle.Source.CommandPath)
	require.Equal(t, "standalone-shortcut", oracle.Source.PositionalClassification)
	require.Len(t, oracle.Boundaries, 3)
	require.Len(t, oracle.Calls, 2)
	for _, call := range oracle.Calls {
		require.Equal(t, "bind", call.Operation)
		require.Equal(t, "builtins.dict", call.ResultType)
		require.Nil(t, call.Error)
	}
	assertNormalizedJSON(t, oracle.Calls[0].Result, oracle.Calls[1].Result)
	return oracle
}

func (o *coreShowObserver) DeleteAnalyzer(ctx context.Context, name string) error {
	if err := o.Client.DeleteAnalyzer(ctx, name); err != nil {
		return err
	}
	o.calls = append(o.calls, normalizedBackendCall{"delete", []string{name}, json.RawMessage("null")})
	return nil
}

func TestCoreDeleteBindingParity(t *testing.T) {
	oracle := loadCoreDeleteOracle(t)
	executions := 0
	for index, call := range oracle.Calls {
		for variant, args := range coreAnalyzerForms("delete", call.Parsed) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(fmt.Sprintf("%d/%d/%s", index, variant, format), func(t *testing.T) {
					executeCoreDeleteBinding(t, oracle, call, args, format)
					executions++
				})
			}
		}
	}
	require.Equal(t, 12, executions)
}

func executeCoreDeleteBinding(
	t *testing.T, oracle coreDeleteOracle, call coreShowSourceCall, args []string, format string,
) {
	t.Helper()
	_ = newDefaultsFixture(t)
	var request struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(call.Result, &request))
	expectedCalls := []normalizedBackendCall{
		{"get", []string{request.Name}, oracle.NativeAnalyzer},
		{"delete", []string{request.Name}, json.RawMessage("null")},
	}
	transport := &recordedTransport{fixture: normalizedHTTPFixture(t, expectedCalls)}
	credential := &recordedCredential{}
	observer := &coreShowObserver{request: json.RawMessage("null"), calls: []normalizedBackendCall{}}
	opens, closes, prompts := 0, 0, 0
	root := newRootCommand(func(
		cmd *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		opens++
		yes, err := cmd.Flags().GetBool("yes")
		require.NoError(t, err)
		require.True(t, yes)
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
				return false, errors.New("Unexpected shared-delete binding prompt.")
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	invocation := append(slices.Clone(args), "--yes", "-o", format, "--no-prompt",
		"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion)
	root.SetArgs(invocation)
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Empty(t, stderr.String())
	require.Zero(t, prompts)
	require.Equal(t, 1, opens)
	require.Equal(t, 1, closes)
	require.Equal(t, 2, credential.calls)
	require.NoError(t, transport.complete())
	assertNormalizedJSON(t, call.Result, observer.request)
	assertNormalizedJSON(t, expectedCalls, observer.calls)
	if format == "none" {
		require.Empty(t, stdout.String())
	} else {
		assertNormalizedJSON(t, oracle.NativeResult, json.RawMessage(stdout.Bytes()))
	}
}
