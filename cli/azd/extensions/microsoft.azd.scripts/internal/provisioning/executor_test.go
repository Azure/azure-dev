// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildShellCommand_Sh(t *testing.T) {
	shell, args := buildShellCommand("sh", "/path/to/script.sh")
	require.NotEmpty(t, shell)
	require.Contains(t, args, "/path/to/script.sh")
}

func TestBuildShellCommand_Pwsh(t *testing.T) {
	shell, args := buildShellCommand("pwsh", "/path/to/script.ps1")
	require.Contains(t, shell, "pwsh")
	require.Contains(t, args, "-NoProfile")
	require.Contains(t, args, "-NonInteractive")
	require.Contains(t, args, "-File")
	require.Contains(t, args, "/path/to/script.ps1")
}

func TestMapToEnvSlice(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}

	slice := mapToEnvSlice(env)
	require.Len(t, slice, 2)
	require.Contains(t, slice, "FOO=bar")
	require.Contains(t, slice, "BAZ=qux")
}

func TestMapToEnvSlice_Empty(t *testing.T) {
	slice := mapToEnvSlice(map[string]string{})
	require.Empty(t, slice)
}
