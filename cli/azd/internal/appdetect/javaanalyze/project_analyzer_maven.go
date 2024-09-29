package javaanalyze

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
)

// mavenProject represents the top-level structure of a Maven POM file.
type mavenProject struct {
	XmlName              xml.Name             `xml:"project"`
	Parent               parent               `xml:"parent"`
	Modules              []string             `xml:"modules>module"` // Capture the modules
	Dependencies         []dependency         `xml:"dependencies>dependency"`
	DependencyManagement dependencyManagement `xml:"dependencyManagement"`
	Build                build                `xml:"build"`
	path                 string
	spring               springProject
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
	//Configuration xml.Node `xml:"configuration"`
}

func analyzeMavenProject(projectPath string) ([]mavenProject, error) {
	rootProject, _ := analyze(projectPath + "/pom.xml")
	var result []mavenProject

	// if it has submodules
	if len(rootProject.Modules) > 0 {
		for _, m := range rootProject.Modules {
			subModule, _ := analyze(projectPath + "/" + m + "/pom.xml")
			result = append(result, *subModule)
		}
	} else {
		result = append(result, *rootProject)
	}
	return result, nil
}

func analyze(filePath string) (*mavenProject, error) {
	xmlFile, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer xmlFile.Close()

	bytes, err := ioutil.ReadAll(xmlFile)
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
