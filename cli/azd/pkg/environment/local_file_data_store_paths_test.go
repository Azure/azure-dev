// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func TestLocalFileDataStore_RejectsLinkedDirectories(t *testing.T) {
	for _, linkKind := range []string{"environment", "base", "dangling"} {
		t.Run(linkKind, func(t *testing.T) {
			for _, operation := range []string{"Get", "Reload", "Save", "Delete", "EnvPath", "ConfigPath", "List"} {
				t.Run(operation, func(t *testing.T) {
					dir := t.TempDir()
					ctx := azdcontext.NewAzdContextWithDirectory(filepath.Join(dir, "low-project"))
					require.NoError(t, os.MkdirAll(ctx.ProjectDirectory(), 0700))
					target := filepath.Join(dir, "trusted-project", ".azure", "prod")
					link := filepath.Join(ctx.EnvironmentDirectory(), "prod")
					if linkKind == "base" {
						target = filepath.Dir(target)
						link = ctx.EnvironmentDirectory()
					} else {
						require.NoError(t, os.Mkdir(ctx.EnvironmentDirectory(), 0700))
					}
					contents := []byte("TRUSTED_MARKER=unchanged\n")
					if linkKind != "dangling" {
						require.NoError(t, os.MkdirAll(target, 0700))
						require.NoError(t, os.WriteFile(filepath.Join(target, DotEnvFileName), contents, 0600))
						require.NoError(t, os.WriteFile(filepath.Join(target, ConfigFileName), []byte("{}"), 0600))
					}
					ostest.DirectoryLink(t, target, link)
					store := NewLocalFileDataStore(ctx, config.NewFileConfigManager(config.NewManager()))

					err := runLinkedPathOperation(t, store, operation)
					if operation == "List" && linkKind != "base" {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, azdcontext.ErrUnsafeEnvironmentPath)
					}
					if linkKind == "dangling" {
						require.NoDirExists(t, target)
					} else {
						actual, err := os.ReadFile(filepath.Join(target, DotEnvFileName))
						require.NoError(t, err)
						require.Equal(t, contents, actual)
						cfg, err := os.ReadFile(filepath.Join(target, ConfigFileName))
						require.NoError(t, err)
						require.Equal(t, "{}", string(cfg))
						entries, err := os.ReadDir(target)
						require.NoError(t, err)
						require.Len(t, entries, 2, "no locks, temp files, or directories should be created")
					}
					_, err = os.Lstat(link)
					require.NoError(t, err, "deletion must not remove the linked entry")
				})
			}
		})
	}
}

func TestLocalFileDataStore_RejectsLinkedFiles(t *testing.T) {
	for _, fileName := range []string{DotEnvFileName, ConfigFileName, DotEnvFileName + ".lock"} {
		t.Run(fileName, func(t *testing.T) {
			for _, operation := range []string{"Get", "Reload", "Save"} {
				t.Run(operation, func(t *testing.T) {
					ctx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
					root, err := ctx.EnvironmentRoot("prod")
					require.NoError(t, err)
					require.NoError(t, os.MkdirAll(root, 0700))
					target := filepath.Join(t.TempDir(), "target")
					contents := []byte("unchanged")
					require.NoError(t, os.WriteFile(target, contents, 0600))
					ostest.Symlink(t, target, filepath.Join(root, fileName))
					store := NewLocalFileDataStore(ctx, config.NewFileConfigManager(config.NewManager()))

					err = runLinkedPathOperation(t, store, operation)
					require.ErrorContains(t, err, "must not be a symbolic link or reparse point")
					actual, err := os.ReadFile(target)
					require.NoError(t, err)
					require.Equal(t, contents, actual)
					entries, err := os.ReadDir(root)
					require.NoError(t, err)
					require.Len(t, entries, 1)
				})
			}
		})
	}
}

func runLinkedPathOperation(t *testing.T, store LocalDataStore, operation string) error {
	t.Helper()
	env := New("prod")
	env.DotenvSet("TRUSTED_MARKER", "modified")
	switch operation {
	case "Get":
		loaded, err := store.Get(t.Context(), "prod")
		require.Nil(t, loaded)
		return err
	case "Reload":
		err := store.Reload(t.Context(), env)
		require.Equal(t, "modified", env.Getenv("TRUSTED_MARKER"))
		return err
	case "Save":
		return store.Save(t.Context(), env, nil)
	case "Delete":
		return store.Delete(t.Context(), "prod")
	case "EnvPath":
		path, err := store.EnvPath(env)
		require.Empty(t, path)
		return err
	case "ConfigPath":
		path, err := store.ConfigPath(env)
		require.Empty(t, path)
		return err
	case "List":
		envs, err := store.List(t.Context())
		require.Empty(t, envs)
		if err == nil {
			require.NotNil(t, envs)
		}
		return err
	default:
		t.Fatalf("unknown operation %q", operation)
		return nil
	}
}

func TestLocalFileDataStore_ListSkipsInvalidEntries(t *testing.T) {
	for _, entryKind := range []string{"invalid-name", "directory-link", "dangling-link", DotEnvFileName, ConfigFileName} {
		t.Run(entryKind, func(t *testing.T) {
			ctx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
			store := NewLocalFileDataStore(ctx, config.NewFileConfigManager(config.NewManager()))
			require.NoError(t, store.Save(t.Context(), New("valid"), nil))
			require.NoError(t, ctx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "valid"}))
			target := filepath.Join(t.TempDir(), "target")
			switch entryKind {
			case "invalid-name":
				require.NoError(t, os.Mkdir(filepath.Join(ctx.EnvironmentDirectory(), "my env"), 0700))
			case "directory-link", "dangling-link":
				if entryKind == "directory-link" {
					require.NoError(t, os.Mkdir(target, 0700))
				}
				ostest.DirectoryLink(t, target, filepath.Join(ctx.EnvironmentDirectory(), "linked"))
			default:
				root := filepath.Join(ctx.EnvironmentDirectory(), "linked")
				require.NoError(t, os.Mkdir(root, 0700))
				require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0600))
				ostest.Symlink(t, target, filepath.Join(root, entryKind))
			}

			envs, err := store.List(t.Context())
			require.NoError(t, err)
			require.Len(t, envs, 1)
			require.Equal(t, "valid", envs[0].Name)
			require.True(t, envs[0].IsDefault)
			if entryKind == "dangling-link" {
				require.NoDirExists(t, target)
			} else if entryKind == DotEnvFileName || entryKind == ConfigFileName {
				raw, err := os.ReadFile(target)
				require.NoError(t, err)
				require.Equal(t, "unchanged", string(raw))
			}
		})
	}
}

func TestLocalFileDataStore_CreateThroughLinkedProject(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "project")
	require.NoError(t, os.Mkdir(target, 0700))
	link := filepath.Join(dir, "linked-project")
	ostest.DirectoryLink(t, target, link)
	ctx := azdcontext.NewAzdContextWithDirectory(link)
	store := NewLocalFileDataStore(ctx, config.NewFileConfigManager(config.NewManager()))
	env := New("prod")
	env.DotenvSet("MARKER", "saved")
	require.NoError(t, store.Save(t.Context(), env, nil))
	loaded, err := store.Get(t.Context(), "prod")
	require.NoError(t, err)
	require.Equal(t, "saved", loaded.Getenv("MARKER"))
	require.NoError(t, store.Delete(t.Context(), "prod"))
	require.NoDirExists(t, filepath.Join(target, ".azure", "prod"))
}
