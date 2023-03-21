package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/javac"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_MavenProject_Restore(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, fmt.Sprintf("%s dependency:resolve", getMvnCmd()))
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.Ephemeral()
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageJava)
	mavenCli := maven.NewMavenCli(mockContext.CommandRunner)
	javaCli := javac.NewCli(mockContext.CommandRunner)

	mavenProject := NewMavenProject(env, mavenCli, javaCli)
	err := mavenProject.Initialize(*mockContext.Context, serviceConfig)
	require.NoError(t, err)

	restoreTask := mavenProject.Restore(*mockContext.Context, serviceConfig)
	logProgress(restoreTask)

	result, err := restoreTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, runArgs.Cmd, getMvnCmd())
	require.Equal(t, serviceConfig.Path(), runArgs.Cwd)
	require.Equal(t,
		[]string{"dependency:resolve"},
		runArgs.Args,
	)
}

func Test_MavenProject_Build(t *testing.T) {
	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, fmt.Sprintf("%s compile", getMvnCmd()))
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.Ephemeral()
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageJava)
	mavenCli := maven.NewMavenCli(mockContext.CommandRunner)
	javaCli := javac.NewCli(mockContext.CommandRunner)

	mavenProject := NewMavenProject(env, mavenCli, javaCli)
	err := mavenProject.Initialize(*mockContext.Context, serviceConfig)
	require.NoError(t, err)

	buildTask := mavenProject.Build(*mockContext.Context, serviceConfig, nil)
	logProgress(buildTask)

	result, err := buildTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, runArgs.Cmd, getMvnCmd())
	require.Equal(t,
		[]string{"compile"},
		runArgs.Args,
	)
}

func Test_MavenProject_Package(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	var runArgs exec.RunArgs

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, fmt.Sprintf("%s package", getMvnCmd()))
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			runArgs = args
			return exec.NewRunResult(0, "", ""), nil
		})

	env := environment.Ephemeral()
	serviceConfig := createTestServiceConfig("./src/api", AppServiceTarget, ServiceLanguageJava)
	mavenCli := maven.NewMavenCli(mockContext.CommandRunner)
	javaCli := javac.NewCli(mockContext.CommandRunner)

	// Simulate a build output with a jar file
	buildOutputDir := filepath.Join(serviceConfig.Path(), "target")
	err := os.MkdirAll(buildOutputDir, osutil.PermissionDirectory)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(buildOutputDir, "test.jar"), []byte("test"), osutil.PermissionFile)
	require.NoError(t, err)

	mavenProject := NewMavenProject(env, mavenCli, javaCli)
	err = mavenProject.Initialize(*mockContext.Context, serviceConfig)
	require.NoError(t, err)

	packageTask := mavenProject.Package(
		*mockContext.Context,
		serviceConfig,
		&ServiceBuildResult{
			BuildOutputPath: serviceConfig.Path(),
		},
	)
	logProgress(packageTask)

	result, err := packageTask.Await()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.PackagePath)
	require.Contains(t, runArgs.Cmd, getMvnCmd())
	require.Equal(t,
		[]string{"package", "-DskipTests"},
		runArgs.Args,
	)
}

func getMvnCmd() string {
	switch runtime.GOOS {
	case "windows":
		return "mvn.cmd"
	case "darwin":
		return "/usr/local/bin/mvn"
	case "linux":
		return "/usr/bin/mvn"
	default:
		panic("OS not supported")
	}
}
