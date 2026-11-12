// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ostest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentHelpersRestoreValues(t *testing.T) {
	const first, second = "AZD_OSTEST_FIRST", "AZD_OSTEST_SECOND"
	t.Setenv(first, "original")
	t.Setenv(second, "original")

	t.Run("set and unset", func(t *testing.T) {
		Setenv(t, first, "single")
		require.Equal(t, "single", os.Getenv(first))
		Setenvs(t, map[string]string{first: "multiple", second: "multiple"})
		require.Equal(t, "multiple", os.Getenv(first))
		require.Equal(t, "multiple", os.Getenv(second))

		Unsetenv(t, first)
		_, exists := os.LookupEnv(first)
		require.False(t, exists)
		Unsetenvs(t, []string{first, second})
		_, exists = os.LookupEnv(second)
		require.False(t, exists)
	})

	require.Equal(t, "original", os.Getenv(first))
	require.Equal(t, "original", os.Getenv(second))
}

func TestCreateCleanup(t *testing.T) {
	for _, removeEarly := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "file")
		t.Run("create", func(t *testing.T) {
			Create(t, path)
			require.FileExists(t, path)
			require.NoError(t, os.WriteFile(path, []byte("contents"), 0600))
			CreateNoCleanup(t, path)
			contents, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Empty(t, contents)
			if removeEarly {
				require.NoError(t, os.Remove(path))
			}
		})
		require.NoFileExists(t, path)
	}
}

func TestChdirRestoresDirectory(t *testing.T) {
	original, err := os.Getwd()
	require.NoError(t, err)
	target, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	t.Run("change", func(t *testing.T) {
		Chdir(t, target)
		current, err := os.Getwd()
		require.NoError(t, err)
		require.Equal(t, target, current)
	})

	current, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, original, current)
}

func TestCombinedPaths(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    string
	}{
		{name: "empty"},
		{name: "unrelated", environ: []string{"HOME=home", "OTHER_PATH=ignored"}},
		{name: "empty path", environ: []string{"PATH="}, want: "PATH="},
		{
			name:    "multiple paths",
			environ: []string{"PATH=first", "HOME=home", "PATH=second"},
			want:    "PATH=first" + string(os.PathListSeparator) + "second",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, CombinedPaths(tt.environ))
		})
	}
}
