// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScaffoldRollbackPreservesOriginalBytesAndPermissions(t *testing.T) {
	for _, original := range []string{"", "\xef\xbb\xbf# preserve BOM and CRLF\r\nx-extra: [one, two]\r\nevals: []\r\n"} {
		t.Run(original, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "custom.yml")
			// #nosec G306 -- exercise preservation of the author's existing permissions.
			require.NoError(t, os.WriteFile(path, []byte(original), 0o640))
			before, err := os.Stat(path)
			require.NoError(t, err)
			unlock, err := LockEvalConfig(t.Context(), path)
			require.NoError(t, err)
			defer unlock()
			undo, err := ApplyScaffoldWithRollback(path, ScaffoldWrite{Evals: []Eval{{Name: "new"}}})
			require.NoError(t, err)
			require.NoError(t, undo())
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, original, string(got))
			after, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, before.Mode().Perm(), after.Mode().Perm())
		})
	}
}

func TestScaffoldRollbackRefusesMissingOrChangedExistingConfig(t *testing.T) {
	for _, change := range []string{"removed", "modified", "directory"} {
		t.Run(change, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, EvalConfigBase)
			require.NoError(t, os.WriteFile(path, []byte("evals: []\n"), 0o600))
			unlock, err := LockEvalConfig(t.Context(), dir)
			require.NoError(t, err)
			defer unlock()
			undo, err := ApplyScaffoldWithRollback(dir, ScaffoldWrite{Evals: []Eval{{Name: "new"}}})
			require.NoError(t, err)
			switch change {
			case "removed":
				require.NoError(t, os.Remove(path))
			case "modified":
				require.NoError(t, os.WriteFile(path, []byte("# another writer\n"), 0o600))
			case "directory":
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0o700))
			}
			require.Error(t, undo())
			switch change {
			case "removed":
				assert.NoFileExists(t, path)
			case "modified":
				got, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, "# another writer\n", string(got))
			case "directory":
				assert.DirExists(t, path)
			}
		})
	}
}

func TestConfigEditsRejectSymlinksWithoutChangingTarget(t *testing.T) {
	for _, operation := range []string{"scaffold", "scaffold with rollback", "catalog", "save", "lock"} {
		for _, dangling := range []bool{false, true} {
			name := operation
			if dangling {
				name += "/dangling"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				target := filepath.Join(dir, "shared.yml")
				original := []byte("# shared config\nevals: []\n")
				if !dangling {
					require.NoError(t, os.WriteFile(target, original, 0o600))
				}
				path := filepath.Join(dir, EvalConfigBase)
				if err := os.Symlink("shared.yml", path); err != nil {
					t.Skipf("creating test symlinks is unavailable: %v", err)
				}
				var err error
				write := ScaffoldWrite{Evals: []Eval{{Name: "new"}}}
				switch operation {
				case "scaffold":
					err = ApplyScaffold(path, write)
				case "scaffold with rollback":
					var undo func() error
					undo, err = ApplyScaffoldWithRollback(path, write)
					assert.Nil(t, undo, "rejection must not require rollback")
				case "catalog":
					_, _, err = UpsertCatalogEntry(path, "datasets", "new", "file", "./rows.jsonl")
				case "save":
					err = SaveEvalConfigTo(path, &EvalConfig{})
				case "lock":
					var unlock func()
					unlock, err = LockEvalConfig(t.Context(), dir)
					if unlock != nil {
						defer unlock()
					}
				}
				require.ErrorContains(t, err, "symbolic link")
				assert.ErrorContains(t, err, "select the target file directly")
				got, err := os.Readlink(path)
				require.NoError(t, err)
				assert.Equal(t, "shared.yml", got)
				if dangling {
					assert.NoFileExists(t, target)
				} else {
					body, err := os.ReadFile(target)
					require.NoError(t, err)
					assert.Equal(t, original, body)
				}
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				wantEntries := 2
				if dangling {
					wantEntries = 1
				}
				assert.Len(t, entries, wantEntries, "no lock, ignore, or temporary files may be created")
			})
		}
	}
}

func TestConfigEditsRejectSelectedDirectorySymlinksWithoutChangingTarget(t *testing.T) {
	for _, operation := range []string{"scaffold", "scaffold with rollback", "catalog", "save", "lock"} {
		for _, existing := range []bool{false, true} {
			for _, trailingSeparator := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/existing=%t/trailing=%t", operation, existing, trailingSeparator), func(t *testing.T) {
					dir := t.TempDir()
					target := filepath.Join(dir, "target")
					require.NoError(t, os.Mkdir(target, 0o700))
					configPath := filepath.Join(target, EvalConfigBase)
					original := []byte("# shared config\nevals: []\n")
					if existing {
						require.NoError(t, os.WriteFile(configPath, original, 0o600))
					}
					link := filepath.Join(dir, "selected directory")
					if err := os.Symlink(target, link); err != nil {
						t.Skipf("creating test symlinks is unavailable: %v", err)
					}
					location := link
					if trailingSeparator {
						location += string(filepath.Separator)
					}
					var err error
					write := ScaffoldWrite{Evals: []Eval{{Name: "new"}}}
					switch operation {
					case "scaffold":
						err = ApplyScaffold(location, write)
					case "scaffold with rollback":
						var undo func() error
						undo, err = ApplyScaffoldWithRollback(location, write)
						assert.Nil(t, undo, "rejection must not require rollback")
					case "catalog":
						_, _, err = UpsertCatalogEntry(location, "datasets", "new", "file", "./rows.jsonl")
					case "save":
						err = SaveEvalConfig(location, &EvalConfig{})
					case "lock":
						var unlock func()
						unlock, err = LockEvalConfig(t.Context(), location)
						if unlock != nil {
							defer unlock()
						}
					}
					assert.ErrorContains(t, err, "symbolic link")
					assert.ErrorContains(t, err, "select the target file directly")
					got, err := os.Readlink(link)
					require.NoError(t, err)
					assert.Equal(t, target, got)
					if existing {
						body, err := os.ReadFile(configPath)
						require.NoError(t, err)
						assert.Equal(t, original, body)
					} else {
						assert.NoFileExists(t, configPath)
					}
					entries, err := os.ReadDir(target)
					require.NoError(t, err)
					wantEntries := 0
					if existing {
						wantEntries = 1
					}
					assert.Len(t, entries, wantEntries, "no lock, ignore, or temporary files may be created")
				})
			}
		}
	}
}
