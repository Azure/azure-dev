package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	config   *ServiceConfig
	env      *environment.Environment
	mavenCli maven.MavenCli
	javacCli javac.JavacCli
}

func (m *mavenProject) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{
		m.mavenCli,
		m.javacCli,
	}
}

func (m *mavenProject) Package(ctx context.Context, progress chan<- string) (string, error) {
	publishRoot, err := os.MkdirTemp("", "azd")
	if err != nil {
		return "", fmt.Errorf("creating staging directory: %w", err)
	}

	progress <- "Creating deployment package"
	if err := m.mavenCli.Package(ctx, m.config.Path()); err != nil {
		return "", err
	}

	publishSource := m.config.Path()

	if m.config.OutputPath != "" {
		publishSource = filepath.Join(publishSource, m.config.OutputPath)
	} else {
		publishSource = filepath.Join(publishSource, "target")
	}

	entries, err := os.ReadDir(publishSource)
	if err != nil {
		return "", fmt.Errorf("discovering JAR files in %s: %w", publishSource, err)
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
		return "", fmt.Errorf("no JAR files found in %s", publishSource)
	}
	if len(matches) > 1 {
		names := strings.Join(matches, ", ")
		return "", fmt.Errorf(
			"multiple JAR files found in %s: %s. Only a single runnable JAR file is expected",
			publishSource,
			names,
		)
	}

	err = copy.Copy(filepath.Join(publishSource, matches[0]), filepath.Join(publishRoot, AppServiceJavaPackageName))
	if err != nil {
		return "", fmt.Errorf("copying to staging directory failed: %w", err)
	}

	return publishRoot, nil
}

func (m *mavenProject) InstallDependencies(ctx context.Context) error {
	if err := m.mavenCli.ResolveDependencies(ctx, m.config.Path()); err != nil {
		return fmt.Errorf("resolving maven dependencies: %w", err)
	}

	return nil
}

func (m *mavenProject) Initialize(ctx context.Context) error {
	return nil
}

func NewMavenProject(
	runner exec.CommandRunner, config *ServiceConfig, env *environment.Environment,
) FrameworkService {
	return &mavenProject{
		config:   config,
		env:      env,
		mavenCli: maven.NewMavenCli(runner, config.Path(), config.Project.Path),
		javacCli: javac.NewCli(runner),
	}
}
