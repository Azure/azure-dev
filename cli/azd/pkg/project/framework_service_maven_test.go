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
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
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

		env := environment.New("test")
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

		env := environment.New("test")
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

		env := environment.New("test")
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

func Test_MavenProject_Package(t *testing.T) {
	type args struct {
		// service config to be packaged.
		svc *ServiceConfig
		// test setup parameter.
		// file extension of java archives to create. empty means no archives are created.
		archivesExt []string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			"Default",
			args{
				&ServiceConfig{
					Project:         &ProjectConfig{},
					Name:            "api",
					RelativePath:    "src/api",
					Host:            AppServiceTarget,
					Language:        ServiceLanguageJava,
					EventDispatcher: ext.NewEventDispatcher[ServiceLifecycleEventArgs](),
				},
				[]string{".jar"},
			},
			false,
		},
		{
			"SpecifyOutputDir",
			args{&ServiceConfig{
				Project:         &ProjectConfig{},
				Name:            "api",
				RelativePath:    "src/api",
				OutputPath:      "mydir",
				Host:            AppServiceTarget,
				Language:        ServiceLanguageJava,
				EventDispatcher: ext.NewEventDispatcher[ServiceLifecycleEventArgs](),
			},
				[]string{".war"},
			},
			false,
		},
		{
			"SpecifyOutputFile",
			args{&ServiceConfig{
				Project:         &ProjectConfig{},
				Name:            "api",
				RelativePath:    "src/api",
				OutputPath:      "mydir/ear.ear",
				Host:            AppServiceTarget,
				Language:        ServiceLanguageJava,
				EventDispatcher: ext.NewEventDispatcher[ServiceLifecycleEventArgs](),
			},
				[]string{".ear"},
			},
			false,
		},
		{
			"ErrNoArchive",
			args{&ServiceConfig{
				Project:         &ProjectConfig{},
				Name:            "api",
				RelativePath:    "src/api",
				Host:            AppServiceTarget,
				Language:        ServiceLanguageJava,
				EventDispatcher: ext.NewEventDispatcher[ServiceLifecycleEventArgs](),
			},
				[]string{},
			},
			true,
		},
		{
			"ErrMultipleArchives",
			args{&ServiceConfig{
				Project:         &ProjectConfig{},
				Name:            "api",
				RelativePath:    "src/api",
				OutputPath:      "mydir",
				Host:            AppServiceTarget,
				Language:        ServiceLanguageJava,
				EventDispatcher: ext.NewEventDispatcher[ServiceLifecycleEventArgs](),
			},
				[]string{".jar", ".war", ".ear"},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			temp := t.TempDir()
			// fill in the project path to avoid setting cwd
			tt.args.svc.Project.Path = temp
			svcDir := filepath.Join(temp, tt.args.svc.RelativePath)
			require.NoError(t, os.MkdirAll(svcDir, osutil.PermissionDirectory))
			err := os.WriteFile(filepath.Join(svcDir, getMvnwCmd()), nil, osutil.PermissionExecutableFile)
			require.NoError(t, err)

			var runArgs exec.RunArgs
			mockContext := mocks.NewMockContext(context.Background())
			mockContext.CommandRunner.
				When(func(args exec.RunArgs, command string) bool {
					return strings.Contains(command, fmt.Sprintf("%s package", getMvnwCmd()))
				}).
				RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					runArgs = args

					packageSrcPath := filepath.Join(svcDir, tt.args.svc.OutputPath)
					if strings.Contains(packageSrcPath, ".") { // an archive file path
						err = os.MkdirAll(filepath.Dir(packageSrcPath), osutil.PermissionDirectory)
						require.NoError(t, err)

						err = os.WriteFile(packageSrcPath, []byte("test"), osutil.PermissionFile)
						require.NoError(t, err)
					} else { // a directory
						if tt.args.svc.OutputPath == "" {
							// default maven target directory
							packageSrcPath = filepath.Join(packageSrcPath, "target")
						}
						err = os.MkdirAll(packageSrcPath, osutil.PermissionDirectory)
						require.NoError(t, err)
						for _, ext := range tt.args.archivesExt {
							err = os.WriteFile(
								// create a file that looks like jar.jar, ear.ear, war.war
								filepath.Join(packageSrcPath, ext[1:]+ext),
								[]byte("test"),
								osutil.PermissionFile)
							require.NoError(t, err)
						}
					}
					return exec.NewRunResult(0, "", ""), nil
				})

			env := environment.New("test")
			mavenCli := maven.NewMavenCli(mockContext.CommandRunner)
			javaCli := javac.NewCli(mockContext.CommandRunner)
			mavenProject := NewMavenProject(env, mavenCli, javaCli)
			err = mavenProject.Initialize(*mockContext.Context, tt.args.svc)
			require.NoError(t, err)

			packageTask := mavenProject.Package(
				*mockContext.Context,
				tt.args.svc,
				&ServiceBuildResult{},
			)
			logProgress(packageTask)

			result, err := packageTask.Await()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.NotEmpty(t, result.PackagePath)
				require.Contains(t, runArgs.Cmd, getMvnwCmd())
				require.Equal(t,
					[]string{"package", "-DskipTests"},
					runArgs.Args,
				)
			}
		})
	}
}

func getMvnwCmd() string {
	if runtime.GOOS == "windows" {
		return "mvnw.cmd"
	} else {
		return "mvnw"
	}
}
