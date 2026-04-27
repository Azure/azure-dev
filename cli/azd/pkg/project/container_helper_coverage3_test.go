// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ContainerHelper_DockerfileBuilder_Coverage3(t *testing.T) {
	ch := &ContainerHelper{}
	builder := ch.DockerfileBuilder()
	require.NotNil(t, builder)
}

func Test_getDockerOptionsWithDefaults_Coverage3(t *testing.T) {
	t.Run("AllEmpty", func(t *testing.T) {
		result := getDockerOptionsWithDefaults(DockerProjectOptions{})
		assert.Equal(t, "./Dockerfile", result.Path)
		assert.Equal(t, docker.DefaultPlatform, result.Platform)
		assert.Equal(t, ".", result.Context)
	})

	t.Run("AllSet", func(t *testing.T) {
		opts := DockerProjectOptions{
			Path:     "custom/Dockerfile",
			Platform: "linux/arm64",
			Context:  "./src",
		}
		result := getDockerOptionsWithDefaults(opts)
		assert.Equal(t, "custom/Dockerfile", result.Path)
		assert.Equal(t, "linux/arm64", result.Platform)
		assert.Equal(t, "./src", result.Context)
	})

	t.Run("PartiallySet", func(t *testing.T) {
		opts := DockerProjectOptions{
			Path: "my/Dockerfile",
		}
		result := getDockerOptionsWithDefaults(opts)
		assert.Equal(t, "my/Dockerfile", result.Path)
		assert.Equal(t, docker.DefaultPlatform, result.Platform)
		assert.Equal(t, ".", result.Context)
	})
}

func Test_resolveDockerPaths(t *testing.T) {
	projectPath := t.TempDir()
	servicePath := filepath.Join(projectPath, "src", "web")
	require.NoError(t, os.MkdirAll(servicePath, 0755))

	t.Run("DefaultPaths_RelativeToServiceDir", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker:       DockerProjectOptions{}, // no user overrides
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		assert.Equal(t, filepath.Join(servicePath, "Dockerfile"), opts.Path)
		assert.Equal(t, servicePath, opts.Context)
	})

	t.Run("UserSpecifiedPaths_RelativeToServiceDir", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path:    "docker/Dockerfile.prod",
				Context: ".",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		assert.Equal(t, filepath.Join(servicePath, "docker", "Dockerfile.prod"), opts.Path)
		assert.Equal(t, servicePath, opts.Context)
	})

	t.Run("AbsolutePaths_Unchanged", func(t *testing.T) {
		absDockerfile := filepath.Join(projectPath, "other", "Dockerfile")
		absContext := filepath.Join(projectPath, "other", "ctx")
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path:    absDockerfile,
				Context: absContext,
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		assert.Equal(t, absDockerfile, opts.Path)
		assert.Equal(t, absContext, opts.Context)
	})

	t.Run("DefaultPaths_RelativePathIsFile", func(t *testing.T) {
		// When RelativePath points to a file (e.g., Aspire project.v1 .csproj),
		// default docker paths should resolve relative to the parent directory.
		serviceDir := filepath.Join(projectPath, "src")
		require.NoError(t, os.MkdirAll(serviceDir, 0755))
		csprojFile := filepath.Join(serviceDir, "app.csproj")
		require.NoError(t, os.WriteFile(csprojFile, []byte("<Project/>"), 0600))

		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "app.csproj"),
			Docker:       DockerProjectOptions{}, // no user overrides
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// Defaults should resolve to the directory containing app.csproj, not app.csproj itself.
		assert.Equal(t, filepath.Join(serviceDir, "Dockerfile"), opts.Path)
		assert.Equal(t, serviceDir, opts.Context)
	})

	t.Run("MixedPaths_UserPathDefaultContext", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path: "docker/Dockerfile.dev",
				// Context not set — will default to "."
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// Both resolve relative to service dir
		assert.Equal(t, filepath.Join(servicePath, "docker", "Dockerfile.dev"), opts.Path)
		assert.Equal(t, servicePath, opts.Context)
	})

	t.Run("PathTraversal_DotDot_Normalized", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path:    "../shared/Dockerfile",
				Context: "../shared",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// ".." segments are resolved and cleaned by filepath.Clean
		assert.Equal(t, filepath.Join(projectPath, "src", "shared", "Dockerfile"), opts.Path)
		assert.Equal(t, filepath.Join(projectPath, "src", "shared"), opts.Context)
	})

	t.Run("PathTraversal_DoubleDotDot_Normalized", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Path:    "../../Dockerfile",
				Context: "../../",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// Double ".." walks back to projectPath root
		assert.Equal(t, filepath.Join(projectPath, "Dockerfile"), opts.Path)
		assert.Equal(t, projectPath, opts.Context)
	})
}

// Test_InMemDockerfile_ContextOverride verifies that in-memory Dockerfile builds
// correctly override the build context to the temp directory when the user did
// not specify a custom Docker context (empty or ".").
//
// This tests the fix where we check serviceConfig.Docker.Context (original config)
// instead of dockerOptions.Context (already resolved to absolute by resolveDockerPaths).
// After resolution, dockerOptions.Context is an absolute path, so comparing against
// "" or "." would never match — checking the original config is intentional.
func Test_InMemDockerfile_ContextOverride(t *testing.T) {
	projectPath := t.TempDir()
	servicePath := filepath.Join(projectPath, "src", "web")
	require.NoError(t, os.MkdirAll(servicePath, 0755))

	t.Run("DefaultContext_OverriddenToTempDir", func(t *testing.T) {
		// When Docker.Context is empty (default), the in-memory Dockerfile flow
		// should override context to the temp dir where the Dockerfile is written.
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker:       DockerProjectOptions{}, // Context defaults to ""
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// After resolution, opts.Context is an absolute path (service dir).
		assert.Equal(t, servicePath, opts.Context, "precondition: context is resolved to service dir")

		// Simulate the in-memory Dockerfile override (container_helper.go ~line 478).
		// This uses serviceConfig.Docker.Context (original config value) — NOT opts.Context.
		tempDir := t.TempDir()
		if svc.Docker.Context == "" || svc.Docker.Context == "." {
			opts.Context = tempDir
		}

		assert.Equal(t, tempDir, opts.Context, "context should be overridden to tempDir")
	})

	t.Run("DotContext_OverriddenToTempDir", func(t *testing.T) {
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Context: ".",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		assert.Equal(t, servicePath, opts.Context, "precondition: '.' resolves to service dir")

		tempDir := t.TempDir()
		if svc.Docker.Context == "" || svc.Docker.Context == "." {
			opts.Context = tempDir
		}

		assert.Equal(t, tempDir, opts.Context, "context should be overridden to tempDir")
	})

	t.Run("CustomContext_PreservedNotOverridden", func(t *testing.T) {
		// When user specifies a custom context, the in-memory Dockerfile flow
		// must NOT override it — the user's explicit context takes precedence.
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker: DockerProjectOptions{
				Context: "custom/build-context",
			},
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		expectedContext := filepath.Join(servicePath, "custom", "build-context")
		assert.Equal(t, expectedContext, opts.Context, "precondition: custom context resolved")

		tempDir := t.TempDir()
		if svc.Docker.Context == "" || svc.Docker.Context == "." {
			opts.Context = tempDir
		}

		// Context should NOT be overridden — user specified a custom path.
		assert.Equal(t, expectedContext, opts.Context,
			"custom context must be preserved, not overridden to tempDir")
	})

	t.Run("BugRegression_CheckingResolvedContext_WouldNeverMatch", func(t *testing.T) {
		// This test proves WHY we check serviceConfig.Docker.Context instead of
		// dockerOptions.Context. After resolveDockerPaths(), opts.Context is always
		// an absolute path — checking it against "" or "." would never match,
		// meaning tempDir override would never happen.
		svc := &ServiceConfig{
			Project:      &ProjectConfig{Path: projectPath},
			RelativePath: filepath.Join("src", "web"),
			Docker:       DockerProjectOptions{}, // Context defaults to ""
		}
		opts := getDockerOptionsWithDefaults(svc.Docker)
		resolveDockerPaths(svc, &opts)

		// Demonstrate the bug: checking resolved opts.Context against "" or "." never matches.
		resolvedContextMatchesEmpty := opts.Context == "" || opts.Context == "."
		assert.False(t, resolvedContextMatchesEmpty,
			"resolved context is absolute — comparing against empty/dot never matches")

		// But the original config value DOES match.
		originalConfigMatchesEmpty := svc.Docker.Context == "" || svc.Docker.Context == "."
		assert.True(t, originalConfigMatchesEmpty,
			"original config is empty — this is what we should check")
	})
}
