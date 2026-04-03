// Copyright (c) Microsoft Corporation. Licensed under the MIT License.
package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- useDotnetPublishForDockerBuild ----

func Test_useDotnetPublishForDockerBuild_Coverage3(t *testing.T) {
	t.Run("CachedTrue", func(t *testing.T) {
		val := true
		sc := &ServiceConfig{
			useDotNetPublishForDockerBuild: &val,
		}
		assert.True(t, useDotnetPublishForDockerBuild(sc))
	})

	t.Run("CachedFalse", func(t *testing.T) {
		val := false
		sc := &ServiceConfig{
			useDotNetPublishForDockerBuild: &val,
		}
		assert.False(t, useDotnetPublishForDockerBuild(sc))
	})

	t.Run("NonDotNet_returns_false", func(t *testing.T) {
		sc := &ServiceConfig{
			Language: ServiceLanguagePython,
			Project:  &ProjectConfig{Path: t.TempDir()},
		}
		result := useDotnetPublishForDockerBuild(sc)
		assert.False(t, result)
		// Should now be cached
		assert.NotNil(t, sc.useDotNetPublishForDockerBuild)
	})

	t.Run("DotNet_WithDockerfile_returns_false", func(t *testing.T) {
		dir := t.TempDir()
		// Create a Dockerfile so that stat succeeds
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch"), 0600))

		sc := &ServiceConfig{
			Language:     ServiceLanguageCsharp,
			Project:      &ProjectConfig{Path: dir},
			RelativePath: ".",
			Docker:       DockerProjectOptions{}, // defaults to "Dockerfile"
		}
		result := useDotnetPublishForDockerBuild(sc)
		assert.False(t, result)
	})

	t.Run("DotNet_NoDockerfile_returns_true", func(t *testing.T) {
		dir := t.TempDir()
		// Do NOT create Dockerfile - the stat should fail

		sc := &ServiceConfig{
			Language:     ServiceLanguageCsharp,
			Project:      &ProjectConfig{Path: dir},
			RelativePath: ".",
			Docker:       DockerProjectOptions{}, // defaults to "Dockerfile"
		}
		result := useDotnetPublishForDockerBuild(sc)
		assert.True(t, result)
	})

	t.Run("DotNet_ProjectPathIsFile_NoDockerfile_returns_true", func(t *testing.T) {
		dir := t.TempDir()
		// Create a .csproj file so Path() points to a file, not a directory
		csproj := filepath.Join(dir, "app.csproj")
		require.NoError(t, os.WriteFile(csproj, []byte("<Project></Project>"), 0600))
		// No Dockerfile in dir

		sc := &ServiceConfig{
			Language:     ServiceLanguageFsharp,
			Project:      &ProjectConfig{Path: dir},
			RelativePath: "app.csproj",
			Docker:       DockerProjectOptions{},
		}
		result := useDotnetPublishForDockerBuild(sc)
		assert.True(t, result)
	})
}

// ---- createDeployableZip ----

func Test_createDeployableZip_Coverage3(t *testing.T) {
	t.Run("EmptyDir", func(t *testing.T) {
		dir := t.TempDir()
		sc := &ServiceConfig{
			Name:    "api",
			Project: &ProjectConfig{Path: dir},
		}
		zipPath, err := createDeployableZip(sc, dir)
		require.NoError(t, err)
		defer os.Remove(zipPath)

		assert.FileExists(t, zipPath)
		assert.Contains(t, zipPath, "api")
	})

	t.Run("DirWithFiles", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>hello</html>"), 0600))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "static"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "static", "app.js"), []byte("console.log('hi')"), 0600))

		sc := &ServiceConfig{
			Name:    "web",
			Project: &ProjectConfig{Path: dir},
		}
		zipPath, err := createDeployableZip(sc, dir)
		require.NoError(t, err)
		defer os.Remove(zipPath)

		info, err := os.Stat(zipPath)
		require.NoError(t, err)
		assert.Greater(t, info.Size(), int64(0))
	})
}
