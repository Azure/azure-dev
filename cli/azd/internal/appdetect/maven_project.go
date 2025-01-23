package appdetect

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

type mavenProject struct {
	pom pom
}

func toMavenProject(ctx context.Context, mvnCli *maven.Cli, pomFilePath string) (mavenProject, error) {
	pom, err := toPom(ctx, mvnCli, pomFilePath)
	if err != nil {
		return mavenProject{}, err
	}
	return mavenProject{pom: pom}, nil
}

func detectDependencies(mavenProject mavenProject, project *Project) (*Project, error) {
	databaseDepMap := map[DatabaseDep]struct{}{}
	for _, dep := range mavenProject.pom.Dependencies {
		if dep.GroupId == "com.mysql" && dep.ArtifactId == "mysql-connector-j" {
			databaseDepMap[DbMySql] = struct{}{}
		}

		if dep.GroupId == "org.postgresql" && dep.ArtifactId == "postgresql" ||
			dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-starter-jdbc-postgresql" {
			databaseDepMap[DbPostgres] = struct{}{}
		}
	}

	if len(databaseDepMap) > 0 {
		project.DatabaseDeps = slices.SortedFunc(maps.Keys(databaseDepMap),
			func(a, b DatabaseDep) int {
				return strings.Compare(string(a), string(b))
			})
	}

	return project, nil
}
