// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	azdcmd "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRegisterGlobalMiddleware(t *testing.T) {
	root := actions.NewActionDescriptor("azd", &actions.ActionDescriptorOptions{})

	registerGlobalMiddleware(root)

	names := make([]string, len(root.Middleware()))
	for i, registration := range root.Middleware() {
		names[i] = registration.Name
	}

	require.Equal(t, []string{"debug", "ux", "telemetry", "error", "loginGuard"}, names)
	require.NotContains(t, names, "toolFirstRun")
	require.NotContains(t, names, "toolUpdateCheck")
}

func TestCommandRequiresLogin(t *testing.T) {
	tests := []struct {
		name     string
		command  *cobra.Command
		required bool
	}{
		{
			name:     "deploy",
			command:  azdcmd.NewDeployCmd(),
			required: true,
		},
		{
			name: "deploy preview",
			command: func() *cobra.Command {
				command := azdcmd.NewDeployCmd()
				azdcmd.NewDeployFlags(command, &internal.GlobalCommandOptions{})
				require.NoError(t, command.Flags().Set("preview", "true"))
				return command
			}(),
			required: true,
		},
		{
			name: "other preview command",
			command: func() *cobra.Command {
				command := &cobra.Command{Use: "provision"}
				command.Flags().Bool("preview", true, "")
				return command
			}(),
			required: true,
		},
		{
			name: "nested deploy command",
			command: func() *cobra.Command {
				root := &cobra.Command{Use: "azd"}
				group := &cobra.Command{Use: "custom"}
				command := &cobra.Command{Use: "deploy"}
				command.Flags().Bool("preview", true, "")
				root.AddCommand(group)
				group.AddCommand(command)
				return command
			}(),
			required: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			descriptor := actions.NewActionDescriptor(tt.command.Name(), &actions.ActionDescriptorOptions{
				Command:      tt.command,
				RequireLogin: true,
			})
			require.Equal(t, tt.required, commandRequiresLogin(descriptor))
		})
	}
}

func TestRootCmd_CwdRelativePathResolvedToAbsolute(t *testing.T) {
	// Regression test: a relative -C path must be resolved to an absolute path
	// before os.Chdir so that the AZD_CWD value propagated to extensions doesn't
	// get double-resolved (once by the root command, once by the extension).
	// See https://github.com/Azure/azure-dev/issues/8229

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	relDir := "mySubFolder"
	absExpected := filepath.Join(tmpDir, relDir)

	container := ioc.NewNestedContainer(nil)
	opts := &internal.GlobalCommandOptions{
		Cwd:      relDir,
		NoPrompt: true,
	}
	ioc.RegisterInstance(container, opts)

	rootCmd := NewRootCmd(false, nil, container)

	// Add a no-op subcommand so cobra has something to run
	rootCmd.SetArgs([]string{"version"})
	rootCmd.SetContext(t.Context())

	// PersistentPreRunE triggers on Execute
	err := rootCmd.Execute()
	require.NoError(t, err)

	require.Equal(t, absExpected, opts.Cwd,
		"relative -C path should be resolved to absolute before chdir")

	// Verify the cobra flag was also updated
	f := rootCmd.PersistentFlags().Lookup("cwd")
	require.NotNil(t, f)
	require.Equal(t, absExpected, f.Value.String(),
		"cobra cwd flag should be updated to absolute path")

	// Note: process CWD is restored by PersistentPostRunE, so we don't check it here.
	// The important thing is that opts.Cwd and the cobra flag hold the absolute path,
	// which is what gets propagated to extensions as AZD_CWD.
}

func TestRootCmd_CwdAbsolutePathUnchanged(t *testing.T) {
	// Absolute paths should pass through unchanged.

	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "absTest")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	container := ioc.NewNestedContainer(nil)
	opts := &internal.GlobalCommandOptions{
		Cwd:      subDir,
		NoPrompt: true,
	}
	ioc.RegisterInstance(container, opts)

	rootCmd := NewRootCmd(false, nil, container)
	rootCmd.SetArgs([]string{"version"})
	rootCmd.SetContext(t.Context())

	err := rootCmd.Execute()
	require.NoError(t, err)

	require.Equal(t, subDir, opts.Cwd,
		"absolute path should remain unchanged")
}
