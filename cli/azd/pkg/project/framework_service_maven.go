package project

import (
	"context"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

type mavenProject struct {
	config   *ServiceConfig
	env      *environment.Environment
	mavenCli maven.MavenCli
}

func (m *mavenProject) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{m.mavenCli}
}

func (m *mavenProject) Package(ctx context.Context, progress chan<- string) (string, error) {
	progress <- "Creating deployment package"
	if err := m.mavenCli.Package(ctx, m.config.Path()); err != nil {
		return "", err
	}

	return filepath.Join(m.config.Path(), "target"), nil
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

func NewMavenProject(ctx context.Context, config *ServiceConfig, env *environment.Environment) FrameworkService {
	return &mavenProject{
		config:   config,
		env:      env,
		mavenCli: maven.NewMavenCli(ctx),
	}
}
