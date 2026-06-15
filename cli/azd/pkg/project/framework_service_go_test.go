// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/golang"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GoProject(t *testing.T) {
	t.Run("Requirements", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		env := environment.New("test")
		goCli := golang.NewCli(mockContext.CommandRunner)

		goProject := NewGoProject(env, goCli)
		reqs := goProject.Requirements()

		require.True(t, reqs.Package.RequireRestore)
		require.True(t, reqs.Package.RequireBuild)
	})

	t.Run("RequiredExternalTools", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		env := environment.New("test")
		goCli := golang.NewCli(mockContext.CommandRunner)

		goProject := NewGoProject(env, goCli)
		tools := goProject.RequiredExternalTools(
			*mockContext.Context, nil,
		)

		require.Len(t, tools, 1)
		require.Equal(t, "Go CLI", tools[0].Name())
	})

	t.Run("Restore", func(t *testing.T) {
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "go mod download")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		env := environment.New("test")
		goCli := golang.NewCli(mockContext.CommandRunner)

		serviceConfig := createTestServiceConfig(
			"./src/api", AzureFunctionTarget, ServiceLanguageGo,
		)

		goProject := NewGoProject(env, goCli)

		serviceContext := NewServiceContext()
		result, err := logProgress(
			t,
			func(p *async.Progress[ServiceProgress]) (
				*ServiceRestoreResult, error,
			) {
				return goProject.Restore(
					*mockContext.Context,
					serviceConfig,
					serviceContext,
					p,
				)
			},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, "go", runArgs.Cmd)
		require.Equal(t, []string{"mod", "download"}, runArgs.Args)
		require.Equal(t, serviceConfig.Path(), runArgs.Cwd)
	})

	t.Run("Build", func(t *testing.T) {
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "go build")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		env := environment.New("test")
		goCli := golang.NewCli(mockContext.CommandRunner)

		serviceConfig := createTestServiceConfig(
			"./src/api", AzureFunctionTarget, ServiceLanguageGo,
		)

		goProject := NewGoProject(env, goCli)

		serviceContext := NewServiceContext()
		result, err := logProgress(
			t,
			func(p *async.Progress[ServiceProgress]) (
				*ServiceBuildResult, error,
			) {
				return goProject.Build(
					*mockContext.Context,
					serviceConfig,
					serviceContext,
					p,
				)
			},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)

		// Verify cross-compilation env vars
		require.Equal(t, "go", runArgs.Cmd)
		require.Equal(t, serviceConfig.Path(), runArgs.Cwd)

		hasGOOS := false
		hasGOARCH := false
		hasCGO := false
		for _, e := range runArgs.Env {
			if e == "GOOS=linux" {
				hasGOOS = true
			}
			if e == "GOARCH=amd64" {
				hasGOARCH = true
			}
			if e == "CGO_ENABLED=0" {
				hasCGO = true
			}
		}
		require.True(t, hasGOOS, "GOOS=linux should be set")
		require.True(t, hasGOARCH, "GOARCH=amd64 should be set")
		require.True(t, hasCGO, "CGO_ENABLED=0 should be set")

		// Verify output path contains "app" binary
		// Args: ["build", "-o", "<path>/app", "."]
		require.Contains(t, runArgs.Args[2], "app")
	})

	t.Run("Package", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		env := environment.New("test")
		goCli := golang.NewCli(mockContext.CommandRunner)

		// Use a temp dir as the project path to avoid polluting source tree
		projectDir := t.TempDir()
		serviceConfig := createTestServiceConfig(
			projectDir, AzureFunctionTarget, ServiceLanguageGo,
		)

		// Create a fake build output directory with app binary
		buildDir := t.TempDir()
		err := os.WriteFile(
			filepath.Join(buildDir, goBinaryName),
			[]byte("fake-binary"),
			osutil.PermissionExecutableFile,
		)
		require.NoError(t, err)

		// Create host.json in the service directory
		svcDir := serviceConfig.Path()
		err = os.WriteFile(
			filepath.Join(svcDir, "host.json"),
			[]byte(`{"version": "2.0"}`),
			osutil.PermissionFile,
		)
		require.NoError(t, err)

		goProject := NewGoProject(env, goCli)

		serviceContext := NewServiceContext()
		serviceContext.Build = ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     buildDir,
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"framework": "go",
				},
			},
		}

		result, err := logProgress(
			t,
			func(p *async.Progress[ServiceProgress]) (
				*ServicePackageResult, error,
			) {
				return goProject.Package(
					*mockContext.Context,
					serviceConfig,
					serviceContext,
					p,
				)
			},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)

		packageDir := result.Artifacts[0].Location

		// Verify binary was copied
		_, err = os.Stat(filepath.Join(packageDir, goBinaryName))
		require.NoError(t, err, "app binary should exist in package")

		// Verify host.json was copied
		_, err = os.Stat(filepath.Join(packageDir, "host.json"))
		require.NoError(t, err, "host.json should exist in package")

		// Verify no worker.config.json — platform provides it on Flex Consumption
		_, err = os.Stat(
			filepath.Join(packageDir, "workers", "golang", "worker.config.json"),
		)
		require.ErrorIs(t, err, os.ErrNotExist,
			"worker.config.json should not exist — platform provides it")
	})
}

func Test_GoProject_Package_NoBuildOutput(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	env := environment.New("test")
	goCli := golang.NewCli(mockContext.CommandRunner)

	serviceConfig := createTestServiceConfig(
		"./src/api", AzureFunctionTarget, ServiceLanguageGo,
	)

	goProject := NewGoProject(env, goCli)

	serviceContext := NewServiceContext()
	// No build artifacts set

	_, err := logProgress(
		t,
		func(p *async.Progress[ServiceProgress]) (
			*ServicePackageResult, error,
		) {
			return goProject.Package(
				*mockContext.Context,
				serviceConfig,
				serviceContext,
				p,
			)
		},
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "no build output found")
}
