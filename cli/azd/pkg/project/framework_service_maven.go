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
		return "", fmt.Errorf("creating package directory for %s: %w", m.config.Name, err)
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
		return "", fmt.Errorf("publishing for %s: %w", m.config.Name, err)
	}

	matches := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.HasSuffix(name, ".jar") || strings.HasSuffix(name, ".war") || strings.HasSuffix(name, ".ear") {
			matches = append(matches, name)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("could not find any .war/.ear/.jar files packaged in %s", publishSource)
	}
	if len(matches) > 1 {
		names := strings.Join(matches, ", ")
		return "", fmt.Errorf("multiple application .war/.ear/.jar files found in %s: %s", publishSource, names)
	}

	err = copy.Copy(filepath.Join(publishSource, matches[0]), filepath.Join(publishRoot, AppServiceJavaPackageName))
	if err != nil {
		return "", fmt.Errorf("publishing for %s: %w", m.config.Name, err)
	}

	return publishRoot, nil
}

func (m *mavenProject) InstallDependencies(ctx context.Context) error {
	if err := m.mavenCli.ResolveDependencies(ctx, m.config.Path()); err != nil {
		return err
	}

	return nil
}

func (m *mavenProject) Initialize(ctx context.Context) error {
	return nil
}

func NewMavenProject(commandRunner exec.CommandRunner, config *ServiceConfig, env *environment.Environment) FrameworkService {
	return &mavenProject{
		config:   config,
		env:      env,
		mavenCli: maven.NewMavenCli(commandRunner, config.Path(), config.Project.Path),
		javacCli: javac.NewCli(commandRunner),
	}
}
