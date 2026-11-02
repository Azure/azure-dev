// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTempDirCanonicalizesSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TMPDIR controls temporary roots on Unix")
	}
	directory := TempDir(t)
	target, alias := filepath.Join(directory, "real"), filepath.Join(directory, "alias")
	require.NoError(t, os.Mkdir(target, 0o700))
	require.NoError(t, os.Symlink(target, alias))
	t.Setenv("TMPDIR", alias)
	t.Run("canonical root", func(t *testing.T) {
		require.True(t, strings.HasPrefix(t.TempDir(), alias+string(filepath.Separator)))
		actual := TempDir(t)
		require.True(t, strings.HasPrefix(actual, target+string(filepath.Separator)))
		resolved, err := filepath.EvalSymlinks(actual)
		require.NoError(t, err)
		require.Equal(t, resolved, actual)
		require.NoError(t, os.WriteFile(filepath.Join(actual, "result"), []byte("private-result"), 0o600))
	})
}
