// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/require"
)

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
