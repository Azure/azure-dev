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
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
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
	npmCli := npm.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)

	npmProject := NewNpmProject(npmCli, env)
	result, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
		serviceContext := NewServiceContext()
		return npmProject.Restore(*mockContext.Context, serviceConfig, serviceContext, progess)
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "npm", runArgs.Cmd)
	require.Equal(t, serviceConfig.Path(), runArgs.Cwd)
	require.Equal(t,
		[]string{"install"},
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
	npmCli := npm.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)

	npmProject := NewNpmProject(npmCli, env)
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
	npmCli := npm.NewCli(mockContext.CommandRunner)
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageTypeScript)
	err := os.MkdirAll(serviceConfig.Path(), osutil.PermissionDirectory)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(serviceConfig.Path(), "package.json"), nil, osutil.PermissionFile)
	require.NoError(t, err)

	npmProject := NewNpmProject(npmCli, env)
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
