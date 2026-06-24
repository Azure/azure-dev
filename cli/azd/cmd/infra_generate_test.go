// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestDetermineDuplicates(t *testing.T) {
	t.Parallel()

	t.Run("no_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create files only in source
		require.NoError(t, os.WriteFile(filepath.Join(source, "main.bicep"), []byte("source"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Empty(t, duplicates)
	})

	t.Run("with_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create same file in both
		require.NoError(t, os.WriteFile(filepath.Join(source, "main.bicep"), []byte("source"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(target, "main.bicep"), []byte("target"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Equal(t, []string{"main.bicep"}, duplicates)
	})

	t.Run("nested_duplicates", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		// Create nested directory structure
		require.NoError(t, os.MkdirAll(filepath.Join(source, "modules"), 0700))
		require.NoError(t, os.MkdirAll(filepath.Join(target, "modules"), 0700))

		require.NoError(t, os.WriteFile(
			filepath.Join(source, "modules", "storage.bicep"), []byte("s"), 0600))
		require.NoError(t, os.WriteFile(
			filepath.Join(target, "modules", "storage.bicep"), []byte("t"), 0600))

		// Also create a non-duplicate
		require.NoError(t, os.WriteFile(
			filepath.Join(source, "main.bicep"), []byte("s"), 0600))

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Len(t, duplicates, 1)
		assert.Contains(t, duplicates[0], "storage.bicep")
	})

	t.Run("empty_source", func(t *testing.T) {
		t.Parallel()

		source := t.TempDir()
		target := t.TempDir()

		duplicates, err := determineDuplicates(source, target)
		require.NoError(t, err)
		assert.Empty(t, duplicates)
	})
}

func Test_NewInfraGenerateAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &infraGenerateFlags{}
	console := mockinput.NewMockConsole()
	calledAs := CmdCalledAs("infra generate")
	a := newInfraGenerateAction(nil, nil, flags, console, nil, nil, calledAs)
	ia := a.(*infraGenerateAction)
	require.Same(t, flags, ia.flags)
	require.Equal(t, calledAs, ia.calledAs)
}

func Test_DetermineDuplicates_NoDuplicates(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.WriteFile(source+"/file1.bicep", []byte("a"), 0600))
	require.NoError(t, os.WriteFile(source+"/file2.bicep", []byte("b"), 0600))

	dups, err := determineDuplicates(source, target)
	require.NoError(t, err)
	require.Empty(t, dups)
}

func Test_DetermineDuplicates_WithDuplicates(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.WriteFile(source+"/file1.bicep", []byte("a"), 0600))
	require.NoError(t, os.WriteFile(source+"/file2.bicep", []byte("b"), 0600))
	require.NoError(t, os.WriteFile(target+"/file1.bicep", []byte("c"), 0600))

	dups, err := determineDuplicates(source, target)
	require.NoError(t, err)
	require.Len(t, dups, 1)
	require.Contains(t, dups, "file1.bicep")
}

func Test_DetermineDuplicates_AllDuplicates(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.WriteFile(source+"/file1.bicep", []byte("a"), 0600))
	require.NoError(t, os.WriteFile(source+"/file2.bicep", []byte("b"), 0600))
	require.NoError(t, os.WriteFile(target+"/file1.bicep", []byte("c"), 0600))
	require.NoError(t, os.WriteFile(target+"/file2.bicep", []byte("d"), 0600))

	dups, err := determineDuplicates(source, target)
	require.NoError(t, err)
	require.Len(t, dups, 2)
}

func Test_NewInfraGenerateFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInfraGenerateFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewInfraGenerateCmd(t *testing.T) {
	t.Parallel()
	cmd := newInfraGenerateCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "generate")
}
