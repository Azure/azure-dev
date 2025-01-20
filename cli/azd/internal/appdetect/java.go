package appdetect

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

type javaDetector struct {
	mvnCli *maven.Cli
}

func (jd *javaDetector) Language() Language {
	return Java
}

func (jd *javaDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "pom.xml" {
			pomFilePath := filepath.Join(path, entry.Name())
			project, err := toMavenProject(ctx, jd.mvnCli, pomFilePath)
			if err != nil {
				return nil, fmt.Errorf("error reading pom.xml: %w", err)
			}

			if len(project.pom.Modules) > 0 {
				// This is a multi-module project, we will capture the analysis, but return nil
				// to continue recursing
				return nil, nil
			}

			result, err := detectDependencies(project, &Project{
				Language:      Java,
				Path:          path,
				DetectionRule: "Inferred by presence of: pom.xml",
			})
			if err != nil {
				return nil, fmt.Errorf("detecting dependencies: %w", err)
			}

			return result, nil
		}
	}

	return nil, nil
}
