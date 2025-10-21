// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
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
	mavenCli *maven.Cli
	javacCli *javac.Cli
}

// NewMavenProject creates a new instance of a maven project
func NewMavenProject(env *environment.Environment, mavenCli *maven.Cli, javaCli *javac.Cli) FrameworkService {
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
func (m *mavenProject) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
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
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	progress.SetProgress(NewServiceProgress("Resolving maven dependencies"))
	if err := m.mavenCli.ResolveDependencies(ctx, serviceConfig.Path()); err != nil {
		return nil, fmt.Errorf("resolving maven dependencies: %w", err)
	}

	// Create restore artifact for the project directory with resolved dependencies
	return &ServiceRestoreResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     serviceConfig.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"projectPath":  serviceConfig.Path(),
					"framework":    "maven",
					"dependencies": ".m2/repository",
				},
			},
		},
	}, nil
}

// Builds the maven project
func (m *mavenProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	progress.SetProgress(NewServiceProgress("Compiling maven project"))
	if err := m.mavenCli.Compile(ctx, serviceConfig.Path()); err != nil {
		return nil, err
	}
	// Create build artifact for maven compile output
	return &ServiceBuildResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     serviceConfig.Path(),
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"buildPath": serviceConfig.Path(),
					"framework": "maven",
					"target":    "target",
				},
			},
		},
	}, nil
}

func (m *mavenProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	progress.SetProgress(NewServiceProgress("Packaging maven project"))
	if err := m.mavenCli.Package(ctx, serviceConfig.Path()); err != nil {
		return nil, err
	}

	if serviceConfig.Host == AzureFunctionTarget {
		if serviceConfig.OutputPath != "" {
			// If the 'dist' property is specified, we use it directly.
			return &ServicePackageResult{
				Artifacts: ArtifactCollection{
					{
						Kind:         ArtifactKindDirectory,
						Location:     filepath.Join(serviceConfig.Path(), serviceConfig.OutputPath),
						LocationKind: LocationKindLocal,
						Metadata: map[string]string{
							"host":      "azure-function",
							"framework": "maven",
						},
					},
				},
			}, nil
		}

		funcAppDir, err := m.funcAppDir(ctx, serviceConfig)
		if err != nil {
			return nil, err
		}

		return &ServicePackageResult{
			Artifacts: ArtifactCollection{
				{
					Kind:         ArtifactKindDirectory,
					Location:     funcAppDir,
					LocationKind: LocationKindLocal,
					Metadata: map[string]string{
						"host":       "azure-function",
						"funcAppDir": funcAppDir,
						"framework":  "maven",
					},
				},
			},
		}, nil
	}

	packageDest, err := os.MkdirTemp("", "azd")
	if err != nil {
		return nil, fmt.Errorf("creating staging directory: %w", err)
	}

	// Get package source path from build artifacts or default to service path
	packageSrcPath := serviceConfig.Path()
	if artifact, found := serviceContext.Build.FindFirst(WithKind(ArtifactKindDirectory)); found {
		packageSrcPath = artifact.Location
	}

	if serviceConfig.OutputPath != "" {
		packageSrcPath = filepath.Join(packageSrcPath, serviceConfig.OutputPath)
	} else {
		packageSrcPath = filepath.Join(packageSrcPath, "target")
	}

	packageSrcFileInfo, err := os.Stat(packageSrcPath)
	if err != nil {
		if serviceConfig.OutputPath == "" {
			return nil, fmt.Errorf("reading default maven target path %s: %w", packageSrcPath, err)
		} else {
			return nil, fmt.Errorf("reading dist path %s: %w", packageSrcPath, err)
		}
	}

	archive := ""
	if packageSrcFileInfo.IsDir() {
		archive, err = m.discoverArchive(packageSrcPath)
		if err != nil {
			return nil, err
		}
	} else {
		archive = packageSrcPath
		if !isSupportedJavaArchive(archive) {
			ext := filepath.Ext(archive)
			return nil, fmt.Errorf(
				"file %s with extension %s is not a supported java archive file (.ear, .war, .jar)", ext, archive)
		}
	}

	progress.SetProgress(NewServiceProgress("Copying deployment package"))
	ext := strings.ToLower(filepath.Ext(archive))
	err = copy.Copy(archive, filepath.Join(packageDest, AppServiceJavaPackageName+ext))
	if err != nil {
		return nil, fmt.Errorf("copying to staging directory failed: %w", err)
	}

	// Create package artifact for maven package output
	return &ServicePackageResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDirectory,
				Location:     packageDest,
				LocationKind: LocationKindLocal,
				Metadata: map[string]string{
					"packageDest": packageDest,
					"framework":   "maven",
				},
			},
		},
	}, nil
}

// funcAppDir returns the directory of the function app packaged by azure-functions-maven-plugin for the given service.
//
// The app is typically packaged under target/azure-functions.
func (m *mavenProject) funcAppDir(ctx context.Context, svc *ServiceConfig) (string, error) {
	svcPath := svc.Path()
	// The staging directory for azure-functions-maven-plugin is target/azure-functions.
	// It isn't configurable, but this may change in the future: https://github.com/microsoft/azure-maven-plugins/issues/1968
	functionsStagingRel := filepath.Join("target", "azure-functions")
	functionsStagingDir := filepath.Join(svcPath, functionsStagingRel)

	// A conventional azure-functions-maven-plugin project will have the property 'functionAppName' in pom.xml,
	// with its property value is passed to azure-functions-maven-plugin as 'appName'.
	appName, err := m.mavenCli.GetProperty(ctx, "functionAppName", svcPath)
	if err != nil && !errors.Is(err, maven.ErrPropertyNotFound) {
		return "", fmt.Errorf("getting 'functionAppName' maven property: %w", err)
	}

	if appName != "" {
		funcDir := filepath.Join(functionsStagingDir, appName)
		if _, err := os.Stat(funcDir); err == nil {
			return funcDir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("checking for function app staging directory: %w", err)
		}
	}

	entries, err := os.ReadDir(functionsStagingDir)
	if err != nil {
		return "", fmt.Errorf("reading azure-functions directory: %w", err)
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}

	if len(dirs) == 1 {
		return filepath.Join(functionsStagingDir, dirs[0]), nil
	}

	for i := range dirs {
		dirs[i] = filepath.Join(functionsStagingRel, dirs[i])
	}

	return "", fmt.Errorf(
		//nolint:lll
		"multiple staging directories found: %s. Specify 'dist' in azure.yaml to select a specific directory",
		strings.Join(dirs, ", "))
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
