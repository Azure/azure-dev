// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func TestStateCacheManager_RejectsLinkedDirectories(t *testing.T) {
	for _, kind := range []string{"base", "environment", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			operations := []string{"Load", "Save", "Invalidate", "GetCachePath"}
			if kind == "base" {
				operations = append(operations, "Touch", "GetTime", "GetStateChangePath")
			}
			for _, operation := range operations {
				t.Run(operation, func(t *testing.T) {
					ctx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
					manager := NewStateCacheManager(ctx)
					target := filepath.Join(t.TempDir(), "target")
					link := filepath.Join(ctx.EnvironmentDirectory(), "prod")
					cacheDir := target
					if kind == "base" {
						link = ctx.EnvironmentDirectory()
						cacheDir = filepath.Join(target, "prod")
					} else {
						require.NoError(t, os.Mkdir(ctx.EnvironmentDirectory(), 0700))
					}
					if kind != "dangling" {
						require.NoError(t, os.MkdirAll(cacheDir, 0700))
						require.NoError(t, os.WriteFile(
							filepath.Join(cacheDir, StateCacheFileName), []byte("unchanged"), 0600))
					}
					ostest.DirectoryLink(t, target, link)

					err := runCachePathOperation(t, manager, operation, "prod")
					require.ErrorIs(t, err, azdcontext.ErrUnsafeEnvironmentPath)
					if kind == "dangling" {
						require.NoDirExists(t, target)
					} else {
						raw, err := os.ReadFile(filepath.Join(cacheDir, StateCacheFileName))
						require.NoError(t, err)
						require.Equal(t, "unchanged", string(raw))
						entries, err := os.ReadDir(target)
						require.NoError(t, err)
						require.Len(t, entries, 1, "must not create a state-change notification")
					}
					_, err = os.Lstat(link)
					require.NoError(t, err)
				})
			}
		})
	}
}

func TestStateCacheManager_RejectsLinkedFiles(t *testing.T) {
	for _, fileName := range []string{StateCacheFileName, StateChangeFileName} {
		t.Run(fileName, func(t *testing.T) {
			operations := []string{"Save", "Invalidate"}
			if fileName == StateCacheFileName {
				operations = append(operations, "Load", "GetCachePath")
			} else {
				operations = append(operations, "Touch", "GetTime", "GetStateChangePath")
			}
			for _, operation := range operations {
				t.Run(operation, func(t *testing.T) {
					ctx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
					manager := NewStateCacheManager(ctx)
					root, err := ctx.EnvironmentRoot("prod")
					require.NoError(t, err)
					require.NoError(t, os.MkdirAll(root, 0700))
					cachePath := filepath.Join(root, StateCacheFileName)
					link := cachePath
					if fileName == StateChangeFileName {
						link = filepath.Join(ctx.EnvironmentDirectory(), StateChangeFileName)
						require.NoError(t, os.WriteFile(cachePath, []byte("local cache"), 0600))
					}
					target := filepath.Join(t.TempDir(), "target")
					require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0600))
					ostest.Symlink(t, target, link)

					err = runCachePathOperation(t, manager, operation, "prod")
					require.ErrorIs(t, err, azdcontext.ErrUnsafeEnvironmentPath)
					raw, err := os.ReadFile(target)
					require.NoError(t, err)
					require.Equal(t, "unchanged", string(raw))
					info, err := os.Lstat(link)
					require.NoError(t, err)
					require.NotZero(t, info.Mode()&os.ModeSymlink)
					if fileName == StateChangeFileName {
						raw, err := os.ReadFile(cachePath)
						require.NoError(t, err)
						require.Equal(t, "local cache", string(raw), "validate notification before changing cache")
					} else {
						require.NoFileExists(t, filepath.Join(ctx.EnvironmentDirectory(), StateChangeFileName))
					}
				})
			}
		})
	}
}

func TestStateCacheManager_RejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../../trusted-project", `..\..\trusted-project`, "my env"} {
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			manager := NewStateCacheManager(azdcontext.NewAzdContextWithDirectory(project))
			for _, operation := range []string{"Load", "Save", "Invalidate", "GetCachePath"} {
				err := runCachePathOperation(t, manager, operation, name)
				require.ErrorContains(t, err, "is invalid")
			}
			entries, err := os.ReadDir(project)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestStateCacheManager_NoProject(t *testing.T) {
	manager := NewStateCacheManager(nil)
	for _, operation := range []string{
		"Load", "Save", "Invalidate", "GetCachePath", "Touch", "GetTime", "GetStateChangePath",
	} {
		require.ErrorIs(t, runCachePathOperation(t, manager, operation, "prod"), azdcontext.ErrNoProject)
	}
}

func TestStateCacheManager_LinkedProject(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "project")
	ostest.DirectoryLink(t, target, link)
	manager := NewStateCacheManager(azdcontext.NewAzdContextWithDirectory(link))
	require.NoError(t, manager.Save(t.Context(), "prod", &StateCache{SubscriptionId: "saved"}))
	cache, err := manager.Load(t.Context(), "prod")
	require.NoError(t, err)
	require.Equal(t, "saved", cache.SubscriptionId)
	require.NoError(t, manager.Invalidate(t.Context(), "prod"))
	require.NoFileExists(t, filepath.Join(target, ".azure", "prod", StateCacheFileName))
	require.FileExists(t, filepath.Join(target, ".azure", StateChangeFileName))
}

func runCachePathOperation(t *testing.T, manager *StateCacheManager, operation, name string) error {
	t.Helper()
	switch operation {
	case "Load":
		cache, err := manager.Load(t.Context(), name)
		require.Nil(t, cache)
		return err
	case "Save":
		return manager.Save(t.Context(), name, &StateCache{SubscriptionId: "modified"})
	case "Invalidate":
		return manager.Invalidate(t.Context(), name)
	case "GetCachePath":
		path, err := manager.GetCachePath(name)
		require.Empty(t, path)
		return err
	case "Touch":
		return manager.TouchStateChange()
	case "GetTime":
		value, err := manager.GetStateChangeTime()
		require.True(t, value.IsZero())
		return err
	case "GetStateChangePath":
		path, err := manager.GetStateChangePath()
		require.Empty(t, path)
		return err
	default:
		t.Fatalf("unknown operation %q", operation)
		return nil
	}
}
