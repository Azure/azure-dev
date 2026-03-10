// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/node"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_NpmProject_Restore(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npm install")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.New("test")
	npmCli := node.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)

	npmProject := NewNodeProject(npmCli, env, mockContext.CommandRunner)
	result, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
		serviceContext := NewServiceContext()
		return npmProject.Restore(*mockContext.Context, serviceConfig, serviceContext, progess)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "npm", runArgs.Cmd)
	require.Equal(t, serviceConfig.Path(), runArgs.Cwd)
	require.Equal(t,
		[]string{"install", "--no-audit", "--no-fund", "--prefer-offline"},
		runArgs.Args,
	)
}

func Test_NpmProject_Build(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npm run build")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.New("test")
	npmCli := node.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)

	npmProject := NewNodeProject(npmCli, env, mockContext.CommandRunner)
	result, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServiceBuildResult, error) {
			return npmProject.Build(*mockContext.Context, serviceConfig, nil, progress)
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "npm", runArgs.Cmd)
	require.Equal(t,
		[]string{"run", "build", "--if-present"},
		runArgs.Args,
	)
}

func Test_NpmProject_Package(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "npm run build")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.New("test")
	npmCli := node.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)
	err := os.MkdirAll(serviceConfig.Path(), osutil.PermissionDirectory)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(serviceConfig.Path(), "package.json"), nil, osutil.PermissionFile)
	require.NoError(t, err)

	npmProject := NewNodeProject(npmCli, env, mockContext.CommandRunner)
	result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
		serviceContext := NewServiceContext()
		serviceContext.Build = ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     serviceConfig.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"framework": "npm",
				},
			},
		}
		return npmProject.Package(
			*mockContext.Context,
			serviceConfig,
			serviceContext,
			progress,
		)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Artifacts[0].Location)
	require.Equal(t, "npm", runArgs.Cmd)
	require.Equal(t,
		[]string{"run", "build", "--if-present"},
		runArgs.Args,
	)
}

func Test_NpmProject_ConfigOverride_Pnpm(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "pnpm install")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.New("test")
	npmCli := node.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)
	serviceConfig.Config = map[string]any{"packageManager": "pnpm"}

	npmProject := NewNodeProject(npmCli, env, mockContext.CommandRunner)
	result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
		serviceContext := NewServiceContext()
		return npmProject.Restore(*mockContext.Context, serviceConfig, serviceContext, progress)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "pnpm", runArgs.Cmd)
	require.Equal(t, []string{"install", "--prefer-offline"}, runArgs.Args)
}

func Test_NpmProject_ConfigOverride_InvalidValue(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	env := environment.New("test")
	npmCli := node.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)
	serviceConfig.Config = map[string]any{"packageManager": "bun"}

	npmProject := NewNodeProject(npmCli, env, mockContext.CommandRunner)
	_, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
		serviceContext := NewServiceContext()
		return npmProject.Restore(*mockContext.Context, serviceConfig, serviceContext, progress)
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid packageManager config value")
}

func Test_NpmProject_ConfigOverride_BeatsDetection(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "yarn")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.New("test")
	npmCli := node.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)

	// Create npm lock file (detection would pick npm)
	err := os.MkdirAll(serviceConfig.Path(), osutil.PermissionDirectory)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(serviceConfig.Path(), "package-lock.json"), []byte("{}"), osutil.PermissionFile)
	require.NoError(t, err)

	// Override to yarn via config
	serviceConfig.Config = map[string]any{"packageManager": "yarn"}

	npmProject := NewNodeProject(npmCli, env, mockContext.CommandRunner)
	result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
		serviceContext := NewServiceContext()
		return npmProject.Restore(*mockContext.Context, serviceConfig, serviceContext, progress)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "yarn", runArgs.Cmd)
	require.Equal(t, []string{"install"}, runArgs.Args)
}
