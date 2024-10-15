package appdetect

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type javaDetector struct {
}

func (jd *javaDetector) Language() Language {
	return Java
}

func (jd *javaDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "pom.xml" {
			project, err := analyzeJavaProject(path)
			if err != nil {
				return nil, err
			}
			return project, nil
		}
	}

	return nil, nil
}

// readMavenProject the java project
func analyzeJavaProject(projectPath string) (*Project, error) {
	var result []Project

	mavenProjects, err := analyzeMavenProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("error analyzing maven project: %w", err)
	}

	for _, mavenProject := range mavenProjects {
		// todo (xiada) we need to add spring related analysis here
		project, err := detectDependencies(&mavenProject, &Project{
			Language:      Java,
			Path:          projectPath,
			DetectionRule: "Inferred by presence of: pom.xml",
		})
		if err != nil {
			return nil, fmt.Errorf("error applying rules: %w", err)
		}
		result = append(result, *project)
	}

	// TODO we should support multiple modules
	return &result[0], nil
}

// mavenProject represents the top-level structure of a Maven POM file.
type mavenProject struct {
	XmlName              xml.Name             `xml:"project"`
	Parent               parent               `xml:"parent"`
	Modules              []string             `xml:"modules>module"` // Capture the modules
	Dependencies         []dependency         `xml:"dependencies>dependency"`
	DependencyManagement dependencyManagement `xml:"dependencyManagement"`
	Build                build                `xml:"build"`
	path                 string
}

// Parent represents the parent POM if this project is a module.
type parent struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
}

// Dependency represents a single Maven dependency.
type dependency struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope,omitempty"`
}

// DependencyManagement includes a list of dependencies that are managed.
type dependencyManagement struct {
	Dependencies []dependency `xml:"dependencies>dependency"`
}

// Build represents the build configuration which can contain plugins.
type build struct {
	Plugins []plugin `xml:"plugins>plugin"`
}

// Plugin represents a build plugin.
type plugin struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
}

func analyzeMavenProject(projectPath string) ([]mavenProject, error) {
	rootProject, err := readMavenProject(filepath.Join(projectPath, "pom.xml"))
	if err != nil {
		return nil, fmt.Errorf("error reading root project: %w", err)
	}
	var result []mavenProject

	// if it has submodules
	if len(rootProject.Modules) > 0 {
		for _, m := range rootProject.Modules {
			subModule, err := readMavenProject(filepath.Join(projectPath, m, "pom.xml"))
			if err != nil {
				return nil, fmt.Errorf("error reading sub module: %w", err)
			}
			result = append(result, *subModule)
		}
	} else {
		result = append(result, *rootProject)
	}
	return result, nil
}

func readMavenProject(filePath string) (*mavenProject, error) {
	bytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	var project mavenProject
	if err := xml.Unmarshal(bytes, &project); err != nil {
		return nil, fmt.Errorf("error parsing XML: %w", err)
	}

	project.path = filepath.Dir(filePath)

	return &project, nil
}

func detectDependencies(mavenProject *mavenProject, project *Project) (*Project, error) {
	databaseDepMap := map[DatabaseDep]struct{}{}
	for _, dep := range mavenProject.Dependencies {
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
