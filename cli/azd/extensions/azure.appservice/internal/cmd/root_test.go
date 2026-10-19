// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSDKRootFlags(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"swap", "--help"}} {
		t.Run(args[0], func(t *testing.T) {
			root := NewRootCommand()
			require.NoError(t, azdext.ValidateNoReservedFlagConflicts(root))
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			require.NoError(t, root.ExecuteContext(t.Context()))
		})
	}
}

func TestSDKGlobalFlagPrecedence(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "environment", true: "explicit"}[explicit], func(t *testing.T) {
			t.Setenv("AZD_DEBUG", "true")
			t.Setenv("AZD_NO_PROMPT", "true")
			t.Setenv("AZD_ENVIRONMENT", "from-env")
			root := NewRootCommand()
			ran := false
			root.AddCommand(&cobra.Command{
				Use: "probe",
				RunE: func(cmd *cobra.Command, args []string) error {
					debug, err := cmd.Flags().GetBool("debug")
					require.NoError(t, err)
					noPrompt, err := cmd.Flags().GetBool("no-prompt")
					require.NoError(t, err)
					environment, err := cmd.Flags().GetString("environment")
					require.NoError(t, err)
					require.Equal(t, !explicit, debug)
					require.Equal(t, !explicit, noPrompt)
					if explicit {
						require.Equal(t, "from-cli", environment)
					} else {
						require.Equal(t, "from-env", environment)
					}
					ran = true
					return nil
				},
			})
			args := []string{"probe"}
			if explicit {
				args = append(args, "--debug=false", "--no-prompt=false", "--environment=from-cli")
			}
			root.SetArgs(args)
			require.NoError(t, root.ExecuteContext(t.Context()))
			require.True(t, ran)
		})
	}
}
