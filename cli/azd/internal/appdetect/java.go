// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

// Regex patterns for Gradle dependency detection
var (
	// Separate patterns for single and double quotes
	groupSingleQuoteRegex = regexp.MustCompile(`group\s*:\s*'([^']+)'`) // Example: group: 'com.example'
	groupDoubleQuoteRegex = regexp.MustCompile(`group\s*:\s*"([^"]+)"`) // Example: group: "com.example"
	nameSingleQuoteRegex  = regexp.MustCompile(`name\s*:\s*'([^']+)'`)  // Example: name: 'library-name'
	nameDoubleQuoteRegex  = regexp.MustCompile(`name\s*:\s*"([^"]+)"`)  // Example: name: "library-name"
)

type javaDetector struct {
	mvnCli       *maven.Cli
	rootProjects []mavenProject
}

func (jd *javaDetector) Language() Language {
	return Java
}

func (jd *javaDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	// First, check for Maven projects (existing code)
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "pom.xml" {
			pomFile := filepath.Join(path, entry.Name())
			project, err := readMavenProject(ctx, jd.mvnCli, pomFile)
			if err != nil {
				return nil, fmt.Errorf("error reading pom.xml: %w", err)
			}

			if len(project.Modules) > 0 {
				// This is a multi-module project, we will capture the analysis, but return nil
				// to continue recursing
				jd.rootProjects = append(jd.rootProjects, *project)
				return nil, nil
			}

			var currentRoot *mavenProject
			for _, rootProject := range jd.rootProjects {
				// we can say that the project is in the root project if the path is under the project
				if inRoot := strings.HasPrefix(pomFile, rootProject.path); inRoot {
					currentRoot = &rootProject
				}
			}

			_ = currentRoot // use currentRoot here in the analysis
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

	// Now, check for Gradle projects
	gradleFiles := []string{"build.gradle", "build.gradle.kts"}

	hasGradleFile := false
	var gradleFilePath string

	for _, entry := range entries {
		name := entry.Name()
		// Check if it's a Gradle build file
		if slices.Contains(gradleFiles, name) {
			hasGradleFile = true
			gradleFilePath = filepath.Join(path, name)
		}
	}

	if hasGradleFile {
		// Create a basic project for Gradle
		project := &Project{
			Language:      Java,
			Path:          path,
			DetectionRule: "Inferred by presence of: build.gradle",
		}
		// Detect dependencies in Gradle project
		result, err := detectGradleDependencies(gradleFilePath, project)
		if err != nil {
			return nil, fmt.Errorf("detecting Gradle dependencies: %w", err)
		}
		return result, nil
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
		if (dep.GroupId == "com.mysql" && dep.ArtifactId == "mysql-connector-j") ||
			(dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-starter-jdbc-mysql") {
			databaseDepMap[DbMySql] = struct{}{}
		}

		if (dep.GroupId == "org.postgresql" && dep.ArtifactId == "postgresql") ||
			(dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-starter-jdbc-postgresql") {
			databaseDepMap[DbPostgres] = struct{}{}
		}

		if (dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-redis") ||
			(dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-redis-reactive") {
			databaseDepMap[DbRedis] = struct{}{}
		}

		if (dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-mongodb") ||
			(dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-mongodb-reactive") {
			databaseDepMap[DbMongo] = struct{}{}
		}
		// todo: Add DbCosmos
	}

	if len(databaseDepMap) > 0 {
		project.DatabaseDeps = slices.SortedFunc(maps.Keys(databaseDepMap),
			func(a, b DatabaseDep) int {
				return strings.Compare(string(a), string(b))
			})
	}

	return project, nil
}

// detectGradleDependencies parses a Gradle build file to identify dependencies
func detectGradleDependencies(filePath string, project *Project) (*Project, error) {
	// Read the build.gradle file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading Gradle file: %w", err)
	}

	fileContent := string(content)

	databaseDepMap := map[DatabaseDep]struct{}{}

	// Parse file line by line to better identify actual dependencies
	// and avoid detecting strings in comments
	lines := strings.Split(fileContent, "\n")
	for _, line := range lines {
		// Check for MySQL dependency patterns
		if isGradleDependency(line, "com.mysql", "mysql-connector-j") {
			databaseDepMap[DbMySql] = struct{}{}
		}

		// Check for PostgreSQL dependency patterns
		if isGradleDependency(line, "org.postgresql", "postgresql") {
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

// isGradleDependency checks if a line contains both groupId and artifactId with common Gradle configuration keywords
func isGradleDependency(line, groupId, artifactId string) bool {
	line = strings.TrimSpace(line)
	// Skip empty lines
	if line == "" {
		return false
	}
	// Skip comment lines
	if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
		return false
	}
	if !strings.Contains(line, artifactId) || !strings.Contains(line, groupId) {
		return false
	}

	// Check for common Gradle dependency configuration keywords
	gradleConfigs := []string{"implementation", "compile", "api", "runtime", "runtimeOnly", "compileOnly"}
	hasConfig := false
	for _, config := range gradleConfigs {
		if strings.Contains(line, config) {
			hasConfig = true
			break
		}
	}

	if !hasConfig {
		return false
	}

	// Check for different Gradle dependency declaration formats

	// Format: implementation 'group:artifact:version'
	// Format: implementation("group:artifact:version")
	colonNotation := fmt.Sprintf("%s:%s", groupId, artifactId)
	if strings.Contains(line, colonNotation) {
		return true
	}

	// Check for group matches with single quotes
	if matches := groupSingleQuoteRegex.FindStringSubmatch(line); len(matches) >= 2 && matches[1] == groupId {
		// Check for name matches with single quotes
		if matches := nameSingleQuoteRegex.FindStringSubmatch(line); len(matches) >= 2 && matches[1] == artifactId {
			return true
		}
	}

	// Check for group matches with double quotes
	if matches := groupDoubleQuoteRegex.FindStringSubmatch(line); len(matches) >= 2 && matches[1] == groupId {
		// Check for name matches with double quotes
		if matches := nameDoubleQuoteRegex.FindStringSubmatch(line); len(matches) >= 2 && matches[1] == artifactId {
			return true
		}
	}

	return false
}
