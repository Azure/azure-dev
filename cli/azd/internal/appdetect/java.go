// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

type javaDetector struct {
	mvnCli         *maven.Cli
	rootProjects   []mavenProject
	moduleProjects map[string]mavenProject
}

func (jd *javaDetector) Language() Language {
	return Java
}

func (jd *javaDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "pom.xml" {
			pomFile := filepath.Join(path, entry.Name())
			project, err := readMavenProject(ctx, jd.mvnCli, pomFile)
			if err != nil {
				return nil, fmt.Errorf("error reading pom.xml: %w", err)
			}

			if len(project.Modules) > 0 {
				// This is a multi-module project, we will capture the analysis, but return nil to continue recursing
				jd.captureRootAndModules(*project, path)
				return nil, nil
			}

			var currentRoot *mavenProject
			for _, rootProject := range jd.rootProjects {
				// We can say that the project is in the root project if
				// 1) the project path is under the root project
				// 2) the project is the module of root project
				underRootProject := strings.HasPrefix(pomFile, filepath.Dir(rootProject.path)+string(filepath.Separator))
				moduleOfRootProject, exist := jd.moduleProjects[project.path]
				if underRootProject && exist && moduleOfRootProject.path == rootProject.path {
					currentRoot = &rootProject
					break
				}
			}

			result, err := detectDependencies(project, &Project{
				Language:      Java,
				Path:          path,
				DetectionRule: "Inferred by presence of: pom.xml",
			})
			if err != nil {
				return nil, fmt.Errorf("detecting dependencies: %w", err)
			}
			if currentRoot != nil {
				result.ParentPath = currentRoot.path
			}

			return result, nil
		}
	}

	return nil, nil
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

func readMavenProject(ctx context.Context, mvnCli *maven.Cli, filePath string) (*mavenProject, error) {
	effectivePom, err := mvnCli.EffectivePom(ctx, filePath)
	if err != nil {
		return nil, err
	}
	var project mavenProject
	if err := xml.Unmarshal([]byte(effectivePom), &project); err != nil {
		return nil, fmt.Errorf("parsing xml: %w", err)
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

// captureRootAndModules records the root and modules projects to detect parent later
func (jd *javaDetector) captureRootAndModules(mavenProject mavenProject, path string) {
	if _, ok := jd.moduleProjects[mavenProject.path]; !ok {
		// Add into root projects if it's new root
		jd.rootProjects = append(jd.rootProjects, mavenProject)
	}
	for _, module := range mavenProject.Modules {
		modulePath := filepath.Join(path, module)
		// modulePath points to the actual root, not current direct parent
		jd.moduleProjects[modulePath] = mavenProject
		for {
			if result, ok := jd.moduleProjects[jd.moduleProjects[modulePath].path]; ok {
				jd.moduleProjects[modulePath] = result
			} else {
				break
			}
		}
	}
}
