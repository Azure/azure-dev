package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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

func NewMavenProject(runner exec.CommandRunner, env *environment.Environment) FrameworkService {
	return &mavenProject{
		env:      env,
		mavenCli: maven.NewMavenCli(runner),
		javacCli: javac.NewCli(runner),
	}
}

func (m *mavenProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{
		m.mavenCli,
		m.javacCli,
	}
}

func (m *mavenProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	m.mavenCli.SetPath(serviceConfig.Path(), serviceConfig.Project.Path)
	return nil
}

func (m *mavenProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
			if err := m.mavenCli.ResolveDependencies(ctx, serviceConfig.Path()); err != nil {
				task.SetError(fmt.Errorf("resolving maven dependencies: %w", err))
				return
			}

			task.SetResult(&ServiceRestoreResult{})
		},
	)
}

func (m *mavenProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			publishRoot, err := os.MkdirTemp("", "azd")
			if err != nil {
				task.SetError(fmt.Errorf("creating staging directory: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Creating deployment package"))
			if err := m.mavenCli.Package(ctx, serviceConfig.Path()); err != nil {
				task.SetError(err)
				return
			}

			publishSource := serviceConfig.Path()

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

			err = copy.Copy(filepath.Join(publishSource, matches[0]), filepath.Join(publishRoot, AppServiceJavaPackageName))
			if err != nil {
				task.SetError(fmt.Errorf("copying to staging directory failed: %w", err))
				return
			}

			task.SetResult(&ServiceBuildResult{
				BuildOutputPath: publishRoot,
			})
		},
	)
}
