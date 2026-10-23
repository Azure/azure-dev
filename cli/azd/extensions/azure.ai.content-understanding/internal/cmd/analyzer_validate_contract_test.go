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

type schemaValidationCase struct {
	Name      string                   `json:"name"`
	Body      string                   `json:"body"`
	WriteFile bool                     `json:"write_file"`
	Strict    bool                     `json:"strict"`
	Result    json.RawMessage          `json:"result"`
	ErrorCode string                   `json:"error_code"`
	Error     *commandErrorExpectation `json:"error"`
}

func TestAnalyzerValidateContracts(t *testing.T) {
	var fixture struct {
		Cases []schemaValidationCase `json:"cases"`
	}
	loadCommandFixture(t, loadParityInventory(t).CoreValidateBinding, &fixture)
	for _, row := range fixture.Cases {
		for _, flag := range []string{"", "--schema", "-s"} {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(fmt.Sprintf("%s/%s/%s", row.Name, flag, format), func(t *testing.T) {
					executeSchemaValidation(t, row, flag, format)
				})
			}
		}
	}
}

func executeSchemaValidation(
	t *testing.T, row schemaValidationCase, flag, format string,
) {
	t.Helper()
	for name, value := range map[string]string{
		"AZD_SERVER": "", "AZD_ACCESS_TOKEN": "private-host-token-marker",
		"CU_ENDPOINT": "private-invalid-endpoint", "CU_API_VERSION": "private-invalid-version",
		"CU_API_KEY": "private-key-marker",
	} {
		t.Setenv(name, value)
	}
	directory := filepath.Join(t.TempDir(), "schema folder with spaces")
	require.NoError(t, os.Mkdir(directory, 0o700))
	path := filepath.Join(directory, "schema.json")
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
		if flag != "" {
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
	require.Equal(t, "schema.json", relative)
	if row.Error != nil {
		require.Error(t, runErr)
		local, ok := errors.AsType[*azdext.LocalError](runErr)
		require.True(t, ok)
		require.Equal(t, row.ErrorCode, local.Code)
		assertJSONEqual(t, row.Error, commandErrorExpectation{
			Category: string(local.Category), Message: local.Message, Hint: &local.Suggestion,
		})
		require.Empty(t, stdout.String())
	} else {
		require.NoError(t, runErr)
		if format == "none" {
			require.Empty(t, stdout.String())
		} else {
			assertJSONEqual(t, row.Result, json.RawMessage(stdout.Bytes()))
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
		require.Equal(t, "schema.json", files[0].Name())
		unchanged, err := local.ReadFile("schema.json")
		require.NoError(t, err)
		require.Equal(t, []byte(row.Body), unchanged)
	} else {
		require.Empty(t, files)
	}
}
