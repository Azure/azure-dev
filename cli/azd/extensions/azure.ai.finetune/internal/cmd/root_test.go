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
	for _, args := range [][]string{{"--help"}, {"init", "--help"}, {"jobs", "show", "--help"}} {
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

func TestInitEnvironmentSelectors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
		err  string
	}{
		{name: "environment default", want: "from-env"},
		{name: "global flag", args: []string{"-e", "from-cli"}, want: "from-cli"},
		{name: "legacy shorthand", args: []string{"-n", "legacy"}, want: "legacy"},
		{name: "matching selectors", args: []string{"-e", "same", "-n", "same"}, want: "same"},
		{name: "conflict", args: []string{"-e", "one", "-n", "two"}, err: "conflicts"},
		{name: "empty global", args: []string{"--environment="}, err: "--environment/-e must not be empty"},
		{name: "empty legacy", args: []string{"--env-name="}, err: "--env-name/-n must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", "from-env")
			root := NewRootCommand()
			cmd, _, err := root.Find([]string{"init"})
			require.NoError(t, err)
			ran := false
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				environment, err := cmd.Flags().GetString("env-name")
				require.NoError(t, err)
				require.Equal(t, tt.want, environment)
				ran = true
				return nil
			}
			root.SetArgs(append([]string{"init"}, tt.args...))
			err = root.ExecuteContext(t.Context())
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				require.False(t, ran)
			} else {
				require.NoError(t, err)
				require.True(t, ran)
			}
		})
	}
}

func TestJobOutputOptions(t *testing.T) {
	tests := []struct {
		name   string
		job    string
		args   []string
		output string
		err    string
	}{
		{name: "list default", job: "list", output: "table"},
		{name: "show default", job: "show", output: "table"},
		{name: "list json", job: "list", args: []string{"-o", "json"}, output: "json"},
		{name: "show yaml", job: "show", args: []string{"--output=yaml"}, output: "yaml"},
		{name: "list yaml rejected", job: "list", args: []string{"--output=yaml"}, err: "invalid value"},
		{name: "empty output rejected", job: "show", args: []string{"--output="}, err: "invalid value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCommand()
			cmd, _, err := root.Find([]string{"jobs", tt.job})
			require.NoError(t, err)
			parentRan := false
			cmd.Parent().PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
				parentRan = true
				output, err := cmd.Flags().GetString("output")
				require.NoError(t, err)
				require.Equal(t, tt.output, output)
				return nil
			}
			ran := false
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				ran = true
				return nil
			}
			args := []string{"jobs", tt.job}
			if tt.job == "show" {
				args = append(args, "--id=test")
			}
			root.SetArgs(append(args, tt.args...))
			err = root.ExecuteContext(t.Context())
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				require.False(t, parentRan, "reject unsupported output before environment initialization")
				require.False(t, ran)
			} else {
				require.NoError(t, err)
				require.True(t, parentRan, "SDK initialization must preserve the jobs pre-run hook")
				require.True(t, ran)
			}
		})
	}
}
