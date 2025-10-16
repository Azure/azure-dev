// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
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
		mavenCli := maven.NewCli(mockContext.CommandRunner)
		javaCli := javac.NewCli(mockContext.CommandRunner)

		mavenProject := NewMavenProject(env, mavenCli, javaCli)
		err = mavenProject.Initialize(*mockContext.Context, serviceConfig)
		require.NoError(t, err)

		serviceContext := NewServiceContext()
		result, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServiceRestoreResult, error) {
			return mavenProject.Restore(*mockContext.Context, serviceConfig, serviceContext, progess)
		})

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
		mavenCli := maven.NewCli(mockContext.CommandRunner)
		javaCli := javac.NewCli(mockContext.CommandRunner)

		mavenProject := NewMavenProject(env, mavenCli, javaCli)
		err = mavenProject.Initialize(*mockContext.Context, serviceConfig)
		require.NoError(t, err)

		result, err := logProgress(
			t, func(progress *async.Progress[ServiceProgress]) (*ServiceBuildResult, error) {
				return mavenProject.Build(*mockContext.Context, serviceConfig, nil, progress)
			},
		)

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
		mavenCli := maven.NewCli(mockContext.CommandRunner)
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

		serviceContext := NewServiceContext()
		serviceContext.Build = ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     serviceConfig.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"framework": "maven",
				},
			},
		}

		result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			return mavenProject.Package(
				*mockContext.Context,
				serviceConfig,
				serviceContext,
				progress,
			)
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)
		require.NotEmpty(t, result.Artifacts[0].Location)
		require.Contains(t, runArgs.Cmd, getMvnwCmd())
		require.Equal(t,
			[]string{"package", "-DskipTests"},
			runArgs.Args,
		)
	})
}

func Test_MavenProject_AppService_Package(t *testing.T) {
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
			mavenCli := maven.NewCli(mockContext.CommandRunner)
			javaCli := javac.NewCli(mockContext.CommandRunner)
			mavenProject := NewMavenProject(env, mavenCli, javaCli)
			err = mavenProject.Initialize(*mockContext.Context, tt.args.svc)
			require.NoError(t, err)

			result, err := logProgress(
				t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
					serviceContext := NewServiceContext()
					return mavenProject.Package(
						*mockContext.Context,
						tt.args.svc,
						serviceContext,
						progress,
					)
				},
			)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, result.Artifacts, 1)
				require.NotEmpty(t, result.Artifacts[0].Location)
				require.Contains(t, runArgs.Cmd, getMvnwCmd())
				require.Equal(t,
					[]string{"package", "-DskipTests"},
					runArgs.Args,
				)
			}
		})
	}
}

func Test_MavenProject_FuncApp_Package(t *testing.T) {
	tempDir := t.TempDir()

	ostest.Chdir(t, tempDir)

	err := os.WriteFile(getMvnwCmd(), nil, osutil.PermissionExecutableFile)
	require.NoError(t, err)

	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, getMvnwCmd()+" package")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "", ""), nil
		})

	// mvnFuncAppNameProperty is the value of the maven property that holds the function app name.
	mvnFuncAppNameProperty := ""
	mockContext.CommandRunner.
		When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, getMvnwCmd()+" help:evaluate -Dexpression=functionAppName")
		}).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			if len(mvnFuncAppNameProperty) == 0 {
				return exec.NewRunResult(0, "", ""), maven.ErrPropertyNotFound
			}

			return exec.NewRunResult(0, mvnFuncAppNameProperty, ""), nil
		})

	env := environment.New("test")
	mavenCli := maven.NewCli(mockContext.CommandRunner)
	javaCli := javac.NewCli(mockContext.CommandRunner)

	serviceConfig := createTestServiceConfig("./src/api", AzureFunctionTarget, ServiceLanguageJava)
	err = os.MkdirAll(serviceConfig.Path(), osutil.PermissionDirectory)
	require.NoError(t, err)

	mavenProject := NewMavenProject(env, mavenCli, javaCli)

	t.Run("uses maven property functionAppName", func(t *testing.T) {
		mvnFuncAppNameProperty = "my-function-app"
		var svc ServiceConfig = *serviceConfig

		err = os.RemoveAll(filepath.Join(svc.Path(), "target", "azure-functions"))
		require.NoError(t, err)

		err = os.MkdirAll(
			filepath.Join(svc.Path(), "target", "azure-functions", mvnFuncAppNameProperty),
			osutil.PermissionDirectory)
		require.NoError(t, err)

		serviceContext := NewServiceContext()
		serviceContext.Build = ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     svc.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"framework": "maven",
				},
			},
		}

		result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			return mavenProject.Package(
				*mockContext.Context,
				&svc,
				serviceContext,
				progress,
			)
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)
		require.Equal(
			t,
			filepath.Join(svc.Path(), "target", "azure-functions", mvnFuncAppNameProperty),
			result.Artifacts[0].Location,
		)
	})

	t.Run("uses target/azure-functions when maven property functionAppName not available", func(t *testing.T) {
		mvnFuncAppNameProperty = ""
		var svc ServiceConfig = *serviceConfig

		err = os.RemoveAll(filepath.Join(svc.Path(), "target", "azure-functions"))
		require.NoError(t, err)

		err = os.MkdirAll(
			filepath.Join(svc.Path(), "target", "azure-functions", "any"),
			osutil.PermissionDirectory)
		require.NoError(t, err)

		serviceContext := NewServiceContext()
		serviceContext.Build = ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     svc.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"framework": "maven",
				},
			},
		}

		result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			return mavenProject.Package(
				*mockContext.Context,
				&svc,
				serviceContext,
				progress,
			)
		})

		// returns single staging directory
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Artifacts, 1)
		require.Equal(t, filepath.Join(svc.Path(), "target", "azure-functions", "any"), result.Artifacts[0].Location)

		// errors when multiple staging directories are found
		err = os.MkdirAll(
			filepath.Join(svc.Path(), "target", "azure-functions", "any2"),
			osutil.PermissionDirectory)
		require.NoError(t, err)

		_, err = logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			serviceContext := NewServiceContext()
			serviceContext.Build = ArtifactCollection{
				{
					Kind:         ArtifactKindDirectory,
					Location:     svc.Path(),
					LocationKind: LocationKindLocal,
					Metadata: map[string]string{
						"framework": "maven",
					},
				},
			}
			return mavenProject.Package(
				*mockContext.Context,
				&svc,
				serviceContext,
				progress,
			)
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "multiple staging directories")
	})

	t.Run("uses dist specified", func(t *testing.T) {
		mvnFuncAppNameProperty = ""
		var svc ServiceConfig = *serviceConfig

		svc.OutputPath = "my-custom-dir"
		result, err := logProgress(t, func(progress *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
			serviceContext := NewServiceContext()
			serviceContext.Build = ArtifactCollection{
				{
					Kind:         ArtifactKindDirectory,
					Location:     svc.Path(),
					LocationKind: LocationKindLocal,
					Metadata: map[string]string{
						"framework": "maven",
					},
				},
			}
			return mavenProject.Package(
				*mockContext.Context,
				&svc,
				serviceContext,
				progress,
			)
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, result.Artifacts[0].Location, filepath.Join(svc.Path(), svc.OutputPath))
	})
}

func getMvnwCmd() string {
	if runtime.GOOS == "windows" {
		return "mvnw.cmd"
	} else {
		return "mvnw"
	}
}
