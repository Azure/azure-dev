// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/stretchr/testify/require"
)

func TestProjectAddACRCapabilityHelpBeforeWrites(t *testing.T) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AZD_SERVER", "invalid-address")
	var out bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"add", "--new-project", "--acr-resource-id", testExternalRegistryID,
		"--help", "--output", "none", "--no-prompt",
	})
	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), "--acr-resource-id")
	require.Contains(t, out.String(), "--new-project")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}
