// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetDefaultEnvironmentName_MissingFile(t *testing.T) {
	t.Parallel()
	ctx := NewAzdContextWithDirectory(t.TempDir())

	name, err := ctx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Empty(t, name)
}

func TestGetDefaultEnvironmentName_HappyPath(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := NewAzdContextWithDirectory(tempDir)

	require.NoError(t, ctx.SetProjectState(ProjectState{DefaultEnvironment: "dev"}))

	name, err := ctx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "dev", name)
}

func TestGetDefaultEnvironmentName_MalformedJSON(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := NewAzdContextWithDirectory(tempDir)

	require.NoError(t, os.MkdirAll(ctx.EnvironmentDirectory(), 0755))
	path := filepath.Join(ctx.EnvironmentDirectory(), ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("not-json"), 0600))

	_, err := ctx.GetDefaultEnvironmentName()
	require.Error(t, err)
	require.Contains(t, err.Error(), "deserializing config file")
}

// A malformed config is no longer repaired by overwriting it, so every caller must report the
// path the user has to remove in order to recover the project.
func TestMalformedConfig_ErrorNamesRecoveryPath(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := NewAzdContextWithDirectory(tempDir)

	require.NoError(t, os.MkdirAll(ctx.EnvironmentDirectory(), 0755))
	path := filepath.Join(ctx.EnvironmentDirectory(), ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("{bad json"), 0600))

	resolved, err := ctx.ProjectStateFilePath(ConfigFileName)
	require.NoError(t, err)

	for name, operation := range map[string]func() error{
		"GetDefaultEnvironmentName": func() error {
			_, err := ctx.GetDefaultEnvironmentName()
			return err
		},
		"SetProjectState": func() error {
			return ctx.SetProjectState(ProjectState{DefaultEnvironment: "prod"})
		},
		"GetCopilotSession": func() error {
			_, err := ctx.GetCopilotSession()
			return err
		},
		"ClearCopilotSession": ctx.ClearCopilotSession,
	} {
		t.Run(name, func(t *testing.T) {
			err := operation()
			require.ErrorContains(t, err, "deserializing config file")
			require.ErrorContains(t, err, resolved)
			require.ErrorContains(t, err, "reset the project state")
		})
	}

	// The failing write must leave the original file untouched rather than silently resetting it.
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{bad json", string(contents))
}

func TestSetProjectState_WritesConfigAndGitignore(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := NewAzdContextWithDirectory(tempDir)

	require.NoError(t, ctx.SetProjectState(ProjectState{DefaultEnvironment: "prod"}))

	// config.json should contain the default environment
	configPath := filepath.Join(ctx.EnvironmentDirectory(), ConfigFileName)
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var cfg configFile
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Equal(t, ConfigFileVersion, cfg.Version)
	require.Equal(t, "prod", cfg.DefaultEnvironment)

	// .gitignore should be written next to the config
	gitignorePath := filepath.Join(ctx.EnvironmentDirectory(), ".gitignore")
	gi, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	require.Contains(t, string(gi), "*")
}

func TestSetProjectState_PreservesCopilotSession(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := NewAzdContextWithDirectory(tempDir)

	sess := &CopilotSession{SessionID: "abc", Command: "up", StartedAt: "now"}
	require.NoError(t, ctx.SetCopilotSession(sess))
	require.NoError(t, ctx.SetProjectState(ProjectState{DefaultEnvironment: "stage"}))

	got, err := ctx.GetCopilotSession()
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, *sess, *got)

	name, err := ctx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "stage", name)
}

func TestCopilotSession_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := NewAzdContextWithDirectory(t.TempDir())

	got, err := ctx.GetCopilotSession()
	require.NoError(t, err)
	require.Nil(t, got, "expected no session before set")

	sess := &CopilotSession{SessionID: "s1", Command: "init", StartedAt: "2026-04-20T00:00:00Z"}
	require.NoError(t, ctx.SetCopilotSession(sess))

	got, err = ctx.GetCopilotSession()
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, *sess, *got)

	require.NoError(t, ctx.ClearCopilotSession())
	got, err = ctx.GetCopilotSession()
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestGetCopilotSession_MalformedConfig(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	ctx := NewAzdContextWithDirectory(tempDir)

	require.NoError(t, os.MkdirAll(ctx.EnvironmentDirectory(), 0755))
	path := filepath.Join(ctx.EnvironmentDirectory(), ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("{not-json"), 0600))

	session, err := ctx.GetCopilotSession()
	require.ErrorContains(t, err, "deserializing config file")
	require.Nil(t, session)
	require.Error(t, ctx.SetCopilotSession(&CopilotSession{SessionID: "new"}))
	require.Error(t, ctx.ClearCopilotSession())
	require.Error(t, ctx.SetProjectState(ProjectState{DefaultEnvironment: "dev"}))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{not-json", string(raw))
}

func TestNewAzdContext_FromProjectDir(t *testing.T) {
	// Not parallel: os.Chdir mutates process-wide cwd, which would race with
	// any other parallel test that reads or writes cwd.
	tempDir := t.TempDir()

	// Resolve the symlink-free absolute path to match what os.Getwd returns
	// when we chdir into tempDir (important on macOS where /tmp is a symlink).
	resolved, err := filepath.EvalSymlinks(tempDir)
	require.NoError(t, err)

	azureYaml := filepath.Join(resolved, "azure.yaml")
	require.NoError(t, os.WriteFile(azureYaml, []byte("name: test\n"), 0600))

	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	require.NoError(t, os.Chdir(resolved))

	ctx, err := NewAzdContext()
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, resolved, ctx.ProjectDirectory())
	require.Equal(t, azureYaml, ctx.ProjectPath())
}
