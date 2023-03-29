package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/javac"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
	"github.com/otiai10/copy"
)

// The default, conventional App Service Java package name
const AppServiceJavaPackageName = "app.jar"

type mavenProject struct {
	env      *environment.Environment
	mavenCli maven.MavenCli
	javacCli javac.JavacCli
}

// NewMavenProject creates a new instance of a maven project
func NewMavenProject(env *environment.Environment, mavenCli maven.MavenCli, javaCli javac.JavacCli) FrameworkService {
	return &mavenProject{
		env:      env,
		mavenCli: mavenCli,
		javacCli: javaCli,
	}
}

func (m *mavenProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		// Maven will automatically restore & build the project if needed
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	}
}

// Gets the required external tools for the project
func (m *mavenProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{
		m.mavenCli,
		m.javacCli,
	}
}

// Initializes the maven project
func (m *mavenProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	m.mavenCli.SetPath(serviceConfig.Path(), serviceConfig.Project.Path)
	return nil
}

// Restores dependencies using the Maven CLI
func (m *mavenProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Resolving maven dependencies"))
			if err := m.mavenCli.ResolveDependencies(ctx, serviceConfig.Path()); err != nil {
				task.SetError(fmt.Errorf("resolving maven dependencies: %w", err))
				return
			}

			task.SetResult(&ServiceRestoreResult{})
		},
	)
}

// Builds the maven project
func (m *mavenProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Compiling maven project"))
			if err := m.mavenCli.Compile(ctx, serviceConfig.Path()); err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServiceBuildResult{
				Restore:         restoreOutput,
				BuildOutputPath: serviceConfig.Path(),
			})
		},
	)
}

func (m *mavenProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			publishRoot, err := os.MkdirTemp("", "azd")
			if err != nil {
				task.SetError(fmt.Errorf("creating staging directory: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Packaging maven project"))
			if err := m.mavenCli.Package(ctx, serviceConfig.Path()); err != nil {
				task.SetError(err)
				return
			}

			publishSource := buildOutput.BuildOutputPath
			if publishSource == "" {
				publishSource = serviceConfig.Path()
			}

			if serviceConfig.OutputPath != "" {
				publishSource = filepath.Join(publishSource, serviceConfig.OutputPath)
			} else {
				publishSource = filepath.Join(publishSource, "target")
			}

			entries, err := os.ReadDir(publishSource)
			if err != nil {
				task.SetError(fmt.Errorf("discovering JAR files in %s: %w", publishSource, err))
				return
			}

			matches := []string{}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}

				if name := entry.Name(); strings.HasSuffix(name, ".jar") {
					matches = append(matches, name)
				}
			}

			if len(matches) == 0 {
				task.SetError(fmt.Errorf("no JAR files found in %s", publishSource))
				return
			}
			if len(matches) > 1 {
				names := strings.Join(matches, ", ")
				task.SetError(fmt.Errorf(
					"multiple JAR files found in %s: %s. Only a single runnable JAR file is expected",
					publishSource,
					names,
				))
				return
			}

			task.SetProgress(NewServiceProgress("Copying deployment package"))
			err = copy.Copy(filepath.Join(publishSource, matches[0]), filepath.Join(publishRoot, AppServiceJavaPackageName))
			if err != nil {
				task.SetError(fmt.Errorf("copying to staging directory failed: %w", err))
				return
			}

			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: publishRoot,
			})
		},
	)
}
