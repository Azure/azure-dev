// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"testing"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestRemoteFlagScope(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_CWD", "")
	t.Setenv("AZD_DEBUG", "false")
	for _, name := range []string{"context", "version", "metadata", "listen"} {
		for _, flag := range []string{"--project-endpoint=", "--project-endpoint=https://example.test", "-p="} {
			t.Run(name+flag, func(t *testing.T) {
				root := NewRootCommand()
				var output bytes.Buffer
				root.SetOut(&output)
				root.SetErr(&output)
				root.SetArgs([]string{flag, name})
				err := root.ExecuteContext(t.Context())
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "expected flag validation, got %v", err)
				require.Equal(t, exterrors.CodeConflictingArguments, local.Code)
				require.Contains(t, local.Message, "--project-endpoint")
				require.Contains(t, local.Message, "skill "+name)
				require.Empty(t, output.String())
			})
		}
	}
}

func TestRemoteFlagScopePreservesCommands(t *testing.T) {
	t.Setenv("AZD_CWD", "")
	t.Setenv("AZD_DEBUG", "false")
	for _, name := range []string{"list", "show", "create", "update", "download", "delete", "context", "version", "metadata"} {
		t.Run(name, func(t *testing.T) {
			root := NewRootCommand()
			command, _, err := root.Find([]string{name})
			require.NoError(t, err)
			command.Args = nil
			command.Flags().VisitAll(func(flag *pflag.Flag) {
				delete(flag.Annotations, cobra.BashCompOneRequiredFlag)
			})
			azdext.RegisterFlagOptions(command, azdext.FlagOptions{
				Name: "output", AllowedValues: []string{"json"}, Default: "json",
			})
			called := false
			command.RunE = func(cmd *cobra.Command, _ []string) error {
				called = true
				require.True(t, cmd.Flags().Changed("no-prompt"))
				format, err := cmd.Flags().GetString("output")
				require.NoError(t, err)
				require.Equal(t, "json", format, "SDK pre-run must apply flag defaults")
				return nil
			}
			args := []string{name, "--no-prompt"}
			if name != "context" && name != "version" && name != "metadata" {
				args = append(args, "--project-endpoint=")
			}
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs(args)
			require.NoError(t, root.ExecuteContext(t.Context()))
			require.True(t, called)
		})
	}
}
