// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentRoot_LinkedDirectories(t *testing.T) {
	for _, targetKind := range []string{"outside", "inside", "dangling"} {
		t.Run(targetKind, func(t *testing.T) {
			dir := t.TempDir()
			ctx := NewAzdContextWithDirectory(filepath.Join(dir, "project"))
			require.NoError(t, os.MkdirAll(ctx.EnvironmentDirectory(), 0700))
			target := filepath.Join(dir, "target")
			if targetKind == "inside" {
				target = filepath.Join(ctx.EnvironmentDirectory(), "target")
			}
			if targetKind != "dangling" {
				require.NoError(t, os.MkdirAll(target, 0700))
			}
			ostest.DirectoryLink(t, target, filepath.Join(ctx.EnvironmentDirectory(), "prod"))

			root, err := ctx.EnvironmentRoot("prod")
			require.ErrorContains(t, err, "must not be a symbolic link or reparse point")
			require.ErrorContains(t, err, "invalid environment path")
			require.ErrorIs(t, err, ErrUnsafeEnvironmentPath)
			require.Empty(t, root)
			if targetKind == "dangling" {
				require.NoDirExists(t, target)
			}
		})
	}
}

func TestEnvironmentRoot_LinkedProjectAncestor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(target, 0700))
	link := filepath.Join(dir, "linked")
	ostest.DirectoryLink(t, target, link)
	canonical, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(target, "existing-project"), 0700))

	for _, suffix := range []string{".", "existing-project", "new-project"} {
		t.Run(suffix, func(t *testing.T) {
			ctx := NewAzdContextWithDirectory(filepath.Join(link, suffix))
			root, err := ctx.EnvironmentRoot("prod")
			require.NoError(t, err)
			require.Equal(t, filepath.Join(canonical, suffix, EnvironmentDirectoryName, "prod"), root)
			require.NoDirExists(t, filepath.Join(target, suffix, EnvironmentDirectoryName))
		})
	}
}

func TestEnvironmentRoot_LongProjectPath(t *testing.T) {
	project := filepath.Join(t.TempDir(), strings.Repeat("a", 100), strings.Repeat("b", 100), strings.Repeat("c", 100))
	require.NoError(t, os.MkdirAll(project, 0700))
	canonical, err := filepath.EvalSymlinks(project)
	require.NoError(t, err)
	ctx := NewAzdContextWithDirectory(project)
	root, err := ctx.EnvironmentRoot("prod")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(canonical, EnvironmentDirectoryName, "prod"), root)
}

func TestEnvironmentPath_ResolutionErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, []byte("unchanged"), 0600))
	ctx := NewAzdContextWithDirectory(file)
	root, err := ctx.EnvironmentRoot("prod")
	require.Error(t, err)
	require.Empty(t, root)

	link := filepath.Join(dir, "dangling")
	ostest.DirectoryLink(t, filepath.Join(dir, "missing"), link)
	ctx = NewAzdContextWithDirectory(filepath.Join(link, "project"))
	root, err = ctx.EnvironmentRoot("prod")
	require.Error(t, err)
	require.Empty(t, root)
	require.NoDirExists(t, filepath.Join(dir, "missing"))
}

func TestEnvironmentFilePath_InvalidFileName(t *testing.T) {
	ctx := NewAzdContextWithDirectory(t.TempDir())
	for _, name := range []string{"", ".", "..", "../file", filepath.Join("sub", "file")} {
		t.Run(name, func(t *testing.T) {
			path, err := ctx.EnvironmentFilePath("prod", name)
			require.ErrorContains(t, err, "invalid environment file name")
			require.Empty(t, path)
			path, err = ctx.ProjectStateFilePath(name)
			require.ErrorContains(t, err, "invalid environment file name")
			require.Empty(t, path)
		})
	}
}

func TestProjectState_RejectsLinkedFiles(t *testing.T) {
	for _, name := range []string{ConfigFileName, ".gitignore"} {
		t.Run(name, func(t *testing.T) {
			ctx := NewAzdContextWithDirectory(t.TempDir())
			require.NoError(t, os.Mkdir(ctx.EnvironmentDirectory(), 0700))
			target := filepath.Join(t.TempDir(), "target")
			contents := []byte(`{"defaultEnvironment":"prod"}`)
			require.NoError(t, os.WriteFile(target, contents, 0600))
			ostest.Symlink(t, target, filepath.Join(ctx.EnvironmentDirectory(), name))

			require.ErrorContains(t, ctx.SetProjectState(ProjectState{DefaultEnvironment: "dev"}),
				"must not be a symbolic link or reparse point")
			if name == ConfigFileName {
				value, err := ctx.GetDefaultEnvironmentName()
				require.ErrorContains(t, err, "must not be a symbolic link or reparse point")
				require.Empty(t, value)
			}

			actual, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, contents, actual)
		})
	}
}

func TestSessionState_RejectsLinks(t *testing.T) {
	for _, kind := range []string{"base", "config", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			for _, operation := range []string{"Get", "Set", "Clear", "SetProjectState"} {
				t.Run(operation, func(t *testing.T) {
					ctx := NewAzdContextWithDirectory(t.TempDir())
					target := filepath.Join(t.TempDir(), "target")
					contents := []byte(`{"defaultEnvironment":"prod","copilotSession":{"sessionId":"trusted"}}`)
					if kind == "base" {
						require.NoError(t, os.Mkdir(target, 0700))
						require.NoError(t, os.WriteFile(filepath.Join(target, ConfigFileName), contents, 0600))
						ostest.DirectoryLink(t, target, ctx.EnvironmentDirectory())
						target = filepath.Join(target, ConfigFileName)
					} else {
						require.NoError(t, os.Mkdir(ctx.EnvironmentDirectory(), 0700))
						if kind != "dangling" {
							require.NoError(t, os.WriteFile(target, contents, 0600))
						}

						ostest.Symlink(t, target, filepath.Join(ctx.EnvironmentDirectory(), ConfigFileName))
					}

					var err error
					switch operation {
					case "Get":
						var session *CopilotSession
						session, err = ctx.GetCopilotSession()
						require.Nil(t, session)
					case "Set":
						err = ctx.SetCopilotSession(&CopilotSession{SessionID: "modified"})
					case "Clear":
						err = ctx.ClearCopilotSession()
					case "SetProjectState":
						err = ctx.SetProjectState(ProjectState{DefaultEnvironment: "dev"})
					}
					require.ErrorIs(t, err, ErrUnsafeEnvironmentPath)
					if kind == "dangling" {
						require.NoFileExists(t, target)
					} else {
						raw, err := os.ReadFile(target)
						require.NoError(t, err)
						require.Equal(t, contents, raw)
					}
					require.NoFileExists(t, filepath.Join(ctx.EnvironmentDirectory(), ".gitignore"))
				})
			}
		})
	}
}

func TestSessionState_LinkedProject(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "project")
	ostest.DirectoryLink(t, target, link)
	ctx := NewAzdContextWithDirectory(link)
	require.NoError(t, ctx.SetProjectState(ProjectState{DefaultEnvironment: "dev"}))
	require.NoError(t, ctx.SetCopilotSession(&CopilotSession{SessionID: "saved"}))
	session, err := ctx.GetCopilotSession()
	require.NoError(t, err)
	require.Equal(t, "saved", session.SessionID)
	require.NoError(t, ctx.ClearCopilotSession())
	name, err := ctx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "dev", name)
}
