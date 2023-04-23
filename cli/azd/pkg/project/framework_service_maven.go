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
const AppServiceJavaPackageName = "app"

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
			packageDest, err := os.MkdirTemp("", "azd")
			if err != nil {
				task.SetError(fmt.Errorf("creating staging directory: %w", err))
				return
			}

			task.SetProgress(NewServiceProgress("Packaging maven project"))
			if err := m.mavenCli.Package(ctx, serviceConfig.Path()); err != nil {
				task.SetError(err)
				return
			}

			packageSrcPath := buildOutput.BuildOutputPath
			if packageSrcPath == "" {
				packageSrcPath = serviceConfig.Path()
			}

			if serviceConfig.OutputPath != "" {
				packageSrcPath = filepath.Join(packageSrcPath, serviceConfig.OutputPath)
			} else {
				packageSrcPath = filepath.Join(packageSrcPath, "target")
			}

			packageSrcFileInfo, err := os.Stat(packageSrcPath)
			if err != nil {
				if serviceConfig.OutputPath == "" {
					task.SetError(fmt.Errorf("reading default maven target path %s: %w", packageSrcPath, err))
				} else {
					task.SetError(fmt.Errorf("reading dist path %s: %w", packageSrcPath, err))
				}
				return
			}

			archive := ""
			if packageSrcFileInfo.IsDir() {
				archive, err = m.discoverArchive(packageSrcPath)
				if err != nil {
					task.SetError(err)
					return
				}
			} else {
				archive = packageSrcPath
				if !isSupportedJavaArchive(archive) {
					ext := filepath.Ext(archive)
					task.SetError(
						fmt.Errorf(
							//nolint:lll
							"file %s with extension %s is not a supported java archive file (.ear, .war, .jar)", ext, archive))
					return
				}
			}

			task.SetProgress(NewServiceProgress("Copying deployment package"))
			ext := strings.ToLower(filepath.Ext(archive))
			err = copy.Copy(archive, filepath.Join(packageDest, AppServiceJavaPackageName+ext))
			if err != nil {
				task.SetError(fmt.Errorf("copying to staging directory failed: %w", err))
				return
			}

			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: packageDest,
			})
		},
	)
}

func isSupportedJavaArchive(archiveFile string) bool {
	ext := strings.ToLower(filepath.Ext(archiveFile))
	return ext == ".jar" || ext == ".war" || ext == ".ear"
}

func (m *mavenProject) discoverArchive(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("discovering java archive files in %s: %w", dir, err)
	}

	archiveFiles := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if isSupportedJavaArchive(name) {
			archiveFiles = append(archiveFiles, name)
		}
	}

	switch len(archiveFiles) {
	case 0:
		return "", fmt.Errorf("no java archive files (.jar, .ear, .war) found in %s", dir)
	case 1:
		return filepath.Join(dir, archiveFiles[0]), nil
	default:
		names := strings.Join(archiveFiles, ", ")
		return "", fmt.Errorf(
			//nolint:lll
			"multiple java archive files (.jar, .ear, .war) found in %s: %s. To pick a specific archive to be used, specify the relative path to the archive file using the 'dist' property in azure.yaml",
			dir,
			names,
		)
	}
}
