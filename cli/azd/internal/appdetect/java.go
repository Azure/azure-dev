package appdetect

import (
	"context"
	"io/fs"
	"log"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

type javaDetector struct {
	parentPoms        []pom
	mavenWrapperPaths []mavenWrapper
}

type mavenWrapper struct {
	posixPath string
	winPath   string
}

// JavaProjectOptionMavenParentPath The parent module path of the maven multi-module project
const JavaProjectOptionMavenParentPath = "parentPath"

// JavaProjectOptionPosixMavenWrapperPath The path to the maven wrapper script for POSIX systems
const JavaProjectOptionPosixMavenWrapperPath = "posixMavenWrapperPath"

// JavaProjectOptionWinMavenWrapperPath The path to the maven wrapper script for Windows systems
const JavaProjectOptionWinMavenWrapperPath = "winMavenWrapperPath"

func (jd *javaDetector) Language() Language {
	return Java
}

func (jd *javaDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "pom.xml" {
			tracing.SetUsageAttributes(fields.AppInitJavaDetect.String("start"))
			pomFile := filepath.Join(path, entry.Name())
			mavenProject, err := toMavenProject(pomFile)
			if err != nil {
				log.Printf("Please edit azure.yaml manually to satisfy your requirement. azd can not help you "+
					"to that by detect your java project because error happened when reading pom.xml: %s. ", err)
				return nil, nil
			}

			if len(mavenProject.pom.Modules) > 0 {
				// This is a multi-module project, we will capture the analysis, but return nil
				// to continue recursing
				jd.parentPoms = append(jd.parentPoms, mavenProject.pom)
				jd.mavenWrapperPaths = append(jd.mavenWrapperPaths, mavenWrapper{
					posixPath: detectMavenWrapper(path, "mvnw"),
					winPath:   detectMavenWrapper(path, "mvnw.cmd"),
				})
				return nil, nil
			}

			var parentPom *pom
			var currentWrapper mavenWrapper
			for i, parentPomItem := range jd.parentPoms {
				// we can say that the project is in the root project if the path is under the project
				if inRoot := strings.HasPrefix(pomFile, parentPomItem.path); inRoot {
					parentPom = &parentPomItem
					currentWrapper = jd.mavenWrapperPaths[i]
					break
				}
			}

			project := Project{
				Language:      Java,
				Path:          path,
				DetectionRule: "Inferred by presence of: pom.xml",
			}
			detectAzureDependenciesByAnalyzingSpringBootProject(parentPom, &mavenProject.pom, &project)
			if parentPom != nil {
				project.Options = map[string]interface{}{
					JavaProjectOptionMavenParentPath:       parentPom.path,
					JavaProjectOptionPosixMavenWrapperPath: currentWrapper.posixPath,
					JavaProjectOptionWinMavenWrapperPath:   currentWrapper.winPath,
				}
			}

			tracing.SetUsageAttributes(fields.AppInitJavaDetect.String("finish"))
			return &project, nil
		}
	}
	return nil, nil
}

func detectMavenWrapper(path string, executable string) string {
	wrapperPath := filepath.Join(path, executable)
	if fileExists(wrapperPath) {
		return wrapperPath
	}
	return ""
}
