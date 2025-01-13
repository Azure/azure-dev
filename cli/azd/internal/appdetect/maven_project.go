package appdetect

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

type mavenProject struct {
	pom pom
}

func createMavenProject(ctx context.Context, mvnCli *maven.Cli, pomFilePath string) (mavenProject, error) {
	pom, err := createEffectivePomOrSimulatedEffectivePom(ctx, mvnCli, pomFilePath)
	if err != nil {
		return mavenProject{}, err
	}
	return mavenProject{
		pom: pom,
	}, nil
}
