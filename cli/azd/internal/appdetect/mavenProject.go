package appdetect

import (
	"maps"
	"slices"
	"strings"
)

type mavenProject struct {
	pom pom
}

func toMavenProject(pomFilePath string) (mavenProject, error) {
	pom, err := toPom(pomFilePath)
	if err != nil {
		return mavenProject{}, nil
	}
	return mavenProject{pom: pom}, nil
}

func detectDependencies(mavenProject mavenProject, project *Project) (*Project, error) {
	databaseDepMap := map[DatabaseDep]struct{}{}
	for _, dep := range mavenProject.pom.Dependencies {
		if dep.GroupId == "com.mysql" && dep.ArtifactId == "mysql-connector-j" {
			databaseDepMap[DbMySql] = struct{}{}
		}

		if dep.GroupId == "org.postgresql" && dep.ArtifactId == "postgresql" {
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
