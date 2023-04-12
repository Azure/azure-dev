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

func Test_MavenProject(t *testing.T) {
	ostest.Chdir(t, t.TempDir())
	require.NoError(t, os.MkdirAll("./src/api", osutil.PermissionDirectory))
	f, err := os.OpenFile(filepath.Join(".", "src", "api", getMvnwCmd()), os.O_CREATE, osutil.PermissionExecutableFile)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	t.Run("Restore", func(t *testing.T) {
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, fmt.Sprintf("%s dependency:resolve", getMvnwCmd()))
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
		err = mavenProject.Initialize(*mockContext.Context, serviceConfig)
		require.NoError(t, err)

		restoreTask := mavenProject.Restore(*mockContext.Context, serviceConfig)
		logProgress(restoreTask)

		result, err := restoreTask.Await()
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Contains(t, runArgs.Cmd, getMvnwCmd())
		require.Equal(t, serviceConfig.Path(), runArgs.Cwd)
		require.Equal(t,
			[]string{"dependency:resolve"},
			runArgs.Args,
		)
	})

	t.Run("Build", func(t *testing.T) {
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, fmt.Sprintf("%s compile", getMvnwCmd()))
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
		err = mavenProject.Initialize(*mockContext.Context, serviceConfig)
		require.NoError(t, err)

		buildTask := mavenProject.Build(*mockContext.Context, serviceConfig, nil)
		logProgress(buildTask)

		result, err := buildTask.Await()
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Contains(t, runArgs.Cmd, getMvnwCmd())
		require.Equal(t,
			[]string{"compile"},
			runArgs.Args,
		)
	})

	t.Run("Package", func(t *testing.T) {
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, fmt.Sprintf("%s package", getMvnwCmd()))
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
		err = os.MkdirAll(buildOutputDir, osutil.PermissionDirectory)
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
		require.Contains(t, runArgs.Cmd, getMvnwCmd())
		require.Equal(t,
			[]string{"package", "-DskipTests"},
			runArgs.Args,
		)
	})
}

func getMvnwCmd() string {
	if runtime.GOOS == "windows" {
		return "mvnw.cmd"
	} else {
		return "mvnw"
	}
}
