// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ostest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSymlink(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		name := "existing"
		if dangling {
			name = "dangling"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target file")
			link := filepath.Join(dir, "linked file")
			if !dangling {
				require.NoError(t, os.WriteFile(target, []byte("original"), 0600))
			}

			Symlink(t, target, link)

			info, err := os.Lstat(link)
			require.NoError(t, err)
			require.NotZero(t, info.Mode()&os.ModeSymlink)
			if dangling {
				_, err := os.Stat(link)
				require.ErrorIs(t, err, os.ErrNotExist)
				require.NoFileExists(t, target)
				return
			}

			contents, err := os.ReadFile(link)
			require.NoError(t, err)
			require.Equal(t, "original", string(contents))
			require.NoError(t, os.WriteFile(link, []byte("updated"), 0600))
			contents, err = os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "updated", string(contents))
		})
	}
}

func TestDirectoryLink(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		name := "existing"
		if dangling {
			name = "dangling"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target directory")
			link := filepath.Join(dir, "linked directory")
			if !dangling {
				require.NoError(t, os.Mkdir(target, 0700))
			}

			DirectoryLink(t, target, link)

			_, err := os.Lstat(link)
			require.NoError(t, err)
			if dangling {
				_, err := os.Stat(link)
				require.ErrorIs(t, err, os.ErrNotExist)
				require.NoDirExists(t, target)
				return
			}

			info, err := os.Stat(link)
			require.NoError(t, err)
			require.True(t, info.IsDir())
			require.NoError(t, os.WriteFile(filepath.Join(link, "marker"), []byte("linked"), 0600))
			contents, err := os.ReadFile(filepath.Join(target, "marker"))
			require.NoError(t, err)
			require.Equal(t, "linked", string(contents))
		})
	}
}
