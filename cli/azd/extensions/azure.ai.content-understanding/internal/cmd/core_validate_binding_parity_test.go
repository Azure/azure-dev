// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type coreValidateInput struct {
	Named      *string `json:"named_schema_path"`
	Positional *string `json:"positional_schema_path"`
}

type coreValidateCall struct {
	Operation       string            `json:"operation"`
	Parsed          coreValidateInput `json:"parsed"`
	Result          json.RawMessage   `json:"result"`
	ResultType      string            `json:"result_type"`
	Error           json.RawMessage   `json:"error"`
	SchemaPathType  string            `json:"schema_path_type"`
	BoundSchemaType string            `json:"bound_schema_type"`
}

type coreValidateCase struct {
	Name      string                 `json:"name"`
	Body      string                 `json:"body"`
	WriteFile bool                   `json:"write_file"`
	Strict    bool                   `json:"strict"`
	Result    json.RawMessage        `json:"result"`
	ErrorCode string                 `json:"error_code"`
	Error     *normalizedParityError `json:"error"`
}

type coreValidateOracle struct {
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
	Calls       []coreValidateCall `json:"calls"`
	NativeCases []coreValidateCase `json:"native_cases"`
	Boundaries  []string           `json:"boundaries"`
}

func loadCoreValidateOracle(t *testing.T) coreValidateOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/core_validate_binding.json", inventory.CoreValidateBinding.File)
	body, err := os.ReadFile("../../testdata/parity/core_validate_binding.json")
	require.NoError(t, err)
	require.Equal(t, inventory.CoreValidateBinding.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle coreValidateOracle
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
		"[path5-canonical5-shortcut5-expected5]", oracle.SourceCase)
	require.Equal(t, []string{"analyzer", "validate"}, oracle.Source.CommandPath)
	require.Equal(t, "standalone-shortcut", oracle.Source.PositionalClassification)
	require.Len(t, oracle.Boundaries, 3)
	require.Len(t, oracle.Calls, 2)
	for _, call := range oracle.Calls {
		require.Equal(t, "bind", call.Operation)
		require.Equal(t, "builtins.dict", call.ResultType)
		require.Equal(t, "pathlib.PosixPath", call.SchemaPathType)
		require.Equal(t, "pathlib.PosixPath", call.BoundSchemaType)
		require.Equal(t, "null", string(call.Error))
		assertNormalizedJSON(t, json.RawMessage(`{"schema":"schema.json"}`), call.Result)
	}
	require.Nil(t, oracle.Calls[0].Parsed.Positional)
	require.NotNil(t, oracle.Calls[0].Parsed.Named)
	require.Equal(t, "schema.json", *oracle.Calls[0].Parsed.Named)
	require.Nil(t, oracle.Calls[1].Parsed.Named)
	require.NotNil(t, oracle.Calls[1].Parsed.Positional)
	require.Equal(t, "schema.json", *oracle.Calls[1].Parsed.Positional)
	names := []string{"clean", "layout-warning", "strict-layout", "malformed-json", "missing-file"}
	require.Len(t, oracle.NativeCases, len(names))
	for index, row := range oracle.NativeCases {
		require.Equal(t, names[index], row.Name)
		require.Equal(t, index >= 2, row.Error != nil)
		require.Equal(t, index >= 2, row.ErrorCode != "")
		require.Equal(t, index != 4, row.WriteFile)
		require.Equal(t, index == 2, row.Strict)
	}
	return oracle
}

func TestCoreValidateBindingParity(t *testing.T) {
	oracle := loadCoreValidateOracle(t)
	executions := 0
	for _, row := range oracle.NativeCases {
		for index, call := range oracle.Calls {
			flags := []string{""}
			if call.Parsed.Named != nil {
				flags = []string{"--schema", "-s"}
			}
			for _, flag := range flags {
				for _, format := range []string{"default", "json", "none"} {
					t.Run(fmt.Sprintf("%s/%d/%s/%s", row.Name, index, flag, format), func(t *testing.T) {
						executeCoreValidateBinding(t, call, row, flag, format)
						executions++
					})
				}
			}
		}
	}
	require.Equal(t, 45, executions)
}

func executeCoreValidateBinding(
	t *testing.T, call coreValidateCall, row coreValidateCase, flag, format string,
) {
	t.Helper()
	for name, value := range map[string]string{
		"AZD_SERVER": "", "AZD_ACCESS_TOKEN": "private-host-token-marker",
		"CU_ENDPOINT": "private-invalid-endpoint", "CU_API_VERSION": "private-invalid-version",
		"CU_API_KEY": "private-key-marker",
	} {
		t.Setenv(name, value)
	}
	var bound struct {
		Schema string `json:"schema"`
	}
	require.NoError(t, json.Unmarshal(call.Result, &bound))
	directory := filepath.Join(t.TempDir(), "schema folder with spaces")
	require.NoError(t, os.Mkdir(directory, 0o700))
	path := filepath.Join(directory, bound.Schema)
	if row.WriteFile {
		require.NoError(t, os.WriteFile(path, []byte(row.Body), 0o600))
	}
	opens, runs := 0, 0
	root := newRootCommand(func(
		*cobra.Command, *azdext.ExtensionContext, config.Options,
	) (*serviceSession, error) {
		opens++
		return nil, errors.New("Offline schema binding opened a service session.")
	})
	command, remaining, err := root.Find([]string{"analyzer", "validate"})
	require.NoError(t, err)
	require.Empty(t, remaining)
	require.NotNil(t, command.RunE)
	original := command.RunE
	selected := ""
	command.RunE = func(cmd *cobra.Command, args []string) error {
		runs++
		if call.Parsed.Named != nil {
			require.Empty(t, args)
			require.True(t, cmd.Flags().Changed("schema"))
			var err error
			selected, err = cmd.Flags().GetString("schema")
			require.NoError(t, err)
		} else {
			require.Len(t, args, 1)
			require.False(t, cmd.Flags().Changed("schema"))
			selected = args[0]
		}
		require.Equal(t, path, selected)
		require.True(t, filepath.IsAbs(selected))
		strict, err := cmd.Flags().GetBool("strict")
		require.NoError(t, err)
		require.Equal(t, row.Strict, strict)
		spec, err := cmd.Flags().GetBool("spec")
		require.NoError(t, err)
		require.False(t, spec)
		version, err := cmd.Flags().GetString("api-version")
		require.NoError(t, err)
		require.Empty(t, version)
		require.False(t, cmd.Flags().Changed("api-version"))
		return original(cmd, args)
	}
	args := []string{"analyzer", "validate"}
	if flag != "" {
		args = append(args, flag)
	}
	args = append(args, path, "-o", format, "--no-prompt")
	if row.Strict {
		args = append(args, "--strict")
	}
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	runErr := root.ExecuteContext(t.Context())
	require.Equal(t, 1, runs)
	require.Zero(t, opens)
	require.Empty(t, stderr.String())
	relative, err := filepath.Rel(directory, selected)
	require.NoError(t, err)
	require.Equal(t, bound.Schema, relative)
	assertNormalizedJSON(t, call.Result, map[string]string{"schema": relative})
	if row.Error != nil {
		require.Error(t, runErr)
		local, ok := errors.AsType[*azdext.LocalError](runErr)
		require.True(t, ok)
		require.Equal(t, row.ErrorCode, local.Code)
		assertNormalizedJSON(t, row.Error, normalizedParityError{
			Category: string(local.Category), Message: local.Message, Hint: &local.Suggestion,
		})
		require.Empty(t, stdout.String())
	} else {
		require.NoError(t, runErr)
		if format == "none" {
			require.Empty(t, stdout.String())
		} else {
			assertNormalizedJSON(t, row.Result, json.RawMessage(stdout.Bytes()))
			require.Contains(t, stdout.String(), cuclient.DefaultAPIVersion)
		}
	}
	local, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer local.Close()
	files, err := fs.ReadDir(local.FS(), ".")
	require.NoError(t, err)
	if row.WriteFile {
		require.Len(t, files, 1)
		require.Equal(t, bound.Schema, files[0].Name())
		unchanged, err := local.ReadFile(bound.Schema)
		require.NoError(t, err)
		require.Equal(t, []byte(row.Body), unchanged)
	} else {
		require.Empty(t, files)
	}
}
