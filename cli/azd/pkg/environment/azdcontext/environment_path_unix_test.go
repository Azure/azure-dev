// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux || darwin

package azdcontext_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestEnvironmentPaths_RejectSpecialFiles(t *testing.T) {
	for _, kind := range []string{"fifo", "socket"} {
		for _, entry := range []string{
			".azure", ".azure/prod", ".azure/prod/wd",
			".azure/prod/.env", ".azure/prod/config.json", ".azure/prod/.env.lock",
			".azure/prod/.state.json", ".azure/config.json", ".azure/.state-change", ".azure/.gitignore",
		} {
			t.Run(kind+"/"+entry, func(t *testing.T) {
				// Unix socket paths have a small fixed limit; keep the temporary root short.
				dir, err := os.MkdirTemp("", "azd-path-")
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
				path := filepath.Join(dir, entry)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
				switch kind {
				case "fifo":
					require.NoError(t, unix.Mkfifo(path, 0600))
				case "socket":
					listener, err := net.Listen("unix", path)
					require.NoError(t, err)
					t.Cleanup(func() { require.NoError(t, listener.Close()) })
				}

				ctx := azdcontext.NewAzdContextWithDirectory(dir)
				var resolved string
				switch entry {
				case ".azure":
					resolved, err = ctx.EnvironmentDirectoryPath()
				case ".azure/prod":
					resolved, err = ctx.EnvironmentRoot("prod")
				case ".azure/prod/wd":
					resolved, err = ctx.GetEnvironmentWorkDirectory("prod")
				default:
					if filepath.Base(filepath.Dir(path)) == "prod" {
						resolved, err = ctx.EnvironmentFilePath("prod", filepath.Base(path))
					} else {
						resolved, err = ctx.ProjectStateFilePath(filepath.Base(path))
					}
				}
				// Check resolution before attempting any I/O that could otherwise block.
				require.ErrorIs(t, err, azdcontext.ErrUnsafeEnvironmentPath)
				require.Empty(t, resolved)

				store := environment.NewLocalFileDataStore(ctx, config.NewFileConfigManager(config.NewManager()))
				switch entry {
				case ".azure/prod/.env", ".azure/prod/config.json", ".azure/prod/.env.lock":
					env, err := store.Get(t.Context(), "prod")
					require.ErrorIs(t, err, azdcontext.ErrUnsafeEnvironmentPath)
					require.Nil(t, env)
					require.NoError(t, store.Save(t.Context(), environment.New("valid"), nil))
					envs, err := store.List(t.Context())
					require.NoError(t, err)
					require.Len(t, envs, 1)
					require.Equal(t, "valid", envs[0].Name)
				case ".azure/prod/.state.json":
					cache, err := state.NewStateCacheManager(ctx).Load(t.Context(), "prod")
					require.ErrorIs(t, err, azdcontext.ErrUnsafeEnvironmentPath)
					require.Nil(t, cache)
				case ".azure/config.json":
					name, err := ctx.GetDefaultEnvironmentName()
					require.ErrorIs(t, err, azdcontext.ErrUnsafeEnvironmentPath)
					require.Empty(t, name)
				case ".azure/.state-change":
					_, err := state.NewStateCacheManager(ctx).GetStateChangeTime()
					require.ErrorIs(t, err, azdcontext.ErrUnsafeEnvironmentPath)
				}
			})
		}
	}
}
