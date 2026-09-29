// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every command that can print "select a specific run with --run" has to
// accept it, or the line sends the reader to a flag that is not there.
func TestEveryRunScopedCommandTakesEvalRun(t *testing.T) {
	root := NewRootCommand()

	paths := []string{
		"run show", "run cancel",
		"run output list", "run output show", "run output export",
	}
	for _, path := range paths {
		cmd, _, err := root.Find(strings.Fields(path))
		require.NoError(t, err, path)
		require.NotNil(t, cmd, path)
		assert.NotNilf(t, cmd.Flags().Lookup("run"),
			"`%s` prints the fallback line, so it must accept --run", path)
	}
}

// The argument names come from the Use line, so a command cannot document one
// thing and demand another.
func TestMissingArgumentIsNamedFromTheUseLine(t *testing.T) {
	cmd := &cobra.Command{Use: "show <output-item>"}

	err := requiredArgs(1)(cmd, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "output-item", "the name comes from Use")
	assert.NotContains(t, err.Error(), "accepts 1 arg")
}

// An optional placeholder is written [run] and is not demanded.
func TestOptionalPlaceholdersAreNotRequired(t *testing.T) {
	cmd := &cobra.Command{Use: "list [run]"}
	assert.Empty(t, argNames(cmd))
}
