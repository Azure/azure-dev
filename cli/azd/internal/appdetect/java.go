package appdetect

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

type javaDetector struct {
	rootProjects []mavenProject
}

func (jd *javaDetector) Language() Language {
	return Java
}

func (jd *javaDetector) DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "pom.xml" {
			tracing.SetUsageAttributes(fields.AppInitJavaDetect.String("start"))
			pomFile := filepath.Join(path, entry.Name())
			project, err := readMavenProject(pomFile)
			if err != nil {
				log.Printf("Please edit azure.yaml manually to satisfy your requirement. azd can not help you "+
					"to that by detect your java project because error happened when reading pom.xml: %s. ", err)
				return nil, nil
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

			result, err := detectDependencies(currentRoot, project, &Project{
				Language:      Java,
				Path:          path,
				DetectionRule: "Inferred by presence of: pom.xml",
			})
			if err != nil {
				log.Printf("Please edit azure.yaml manually to satisfy your requirement. azd can not help you "+
					"to that by detect your java project because error happened when detecting dependencies: %s", err)
				return nil, nil
			}

			tracing.SetUsageAttributes(fields.AppInitJavaDetect.String("finish"))
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
	Properties           Properties           `xml:"properties"`
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

type Properties struct {
	Entries []Property `xml:",any"` // Capture all elements inside <properties>
}

type Property struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
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

func readMavenProject(filePath string) (*mavenProject, error) {
	bytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var initialProject mavenProject
	if err := xml.Unmarshal(bytes, &initialProject); err != nil {
		return nil, fmt.Errorf("parsing xml: %w", err)
	}

	// replace all placeholders with properties
	str := replaceAllPlaceholders(initialProject, string(bytes))

	var project mavenProject
	if err := xml.Unmarshal([]byte(str), &project); err != nil {
		return nil, fmt.Errorf("parsing xml: %w", err)
	}

	project.path = filepath.Dir(filePath)

	return &project, nil
}

func replaceAllPlaceholders(project mavenProject, input string) string {
	propsMap := parseProperties(project.Properties)

	re := regexp.MustCompile(`\$\{([A-Za-z0-9-_.]+)}`)
	return re.ReplaceAllStringFunc(input, func(match string) string {
		// Extract the key inside ${}
		key := re.FindStringSubmatch(match)[1]
		if value, exists := propsMap[key]; exists {
			return value
		}
		return match
	})
}

func parseProperties(properties Properties) map[string]string {
	result := make(map[string]string)
	for _, entry := range properties.Entries {
		result[entry.XMLName.Local] = entry.Value
	}
	return result
}

func detectDependencies(currentRoot *mavenProject, mavenProject *mavenProject, project *Project) (*Project, error) {
	detectAzureDependenciesByAnalyzingSpringBootProject(currentRoot, mavenProject, project)
	return project, nil
}
