// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"azure.ai.routines/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestRemoteFlagScope(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_CWD", "")
	for _, args := range [][]string{{"context"}, {"version"}, {"metadata"}, {"listen"}, {"add", "test"}} {
		for _, flag := range []string{
			"--project-endpoint=", "--project-endpoint=https://example.test", "-p=",
			"--timeout=", "--timeout=0", "--timeout=90s",
		} {
			t.Run(strings.Join(args, " ")+flag, func(t *testing.T) {
				root := NewRootCommand()
				var output bytes.Buffer
				root.SetOut(&output)
				root.SetErr(&output)
				root.SetArgs(append([]string{flag}, args...))
				err := root.ExecuteContext(t.Context())
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "expected flag validation, got %v", err)
				require.Equal(t, exterrors.CodeConflictingArguments, local.Code)
				require.Contains(t, local.Message, "routine "+args[0])
				require.Contains(t, local.Message, "is not supported")
				require.Empty(t, output.String())
			})
		}
	}
}

func TestRemoteFlagScopePreservesCommands(t *testing.T) {
	t.Setenv("AZD_CWD", "")
	for _, path := range [][]string{
		{"create"}, {"update"}, {"show"}, {"list"}, {"delete"}, {"enable"}, {"disable"},
		{"dispatch"}, {"run", "list"}, {"context"}, {"version"}, {"metadata"}, {"add"},
	} {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root := NewRootCommand()
			command, _, err := root.Find(path)
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
			args := append([]string{}, path...)
			args = append(args, "--no-prompt")
			switch path[0] {
			case "context", "version", "metadata", "add":
			default:
				args = append(args, "--project-endpoint=", "--timeout=90s")
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
