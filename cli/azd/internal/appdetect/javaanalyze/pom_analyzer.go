package javaanalyze

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"os"
)

// MavenProject represents the top-level structure of a Maven POM file.
type MavenProject struct {
	XMLName              xml.Name             `xml:"project"`
	Parent               Parent               `xml:"parent"`
	Modules              []string             `xml:"modules>module"` // Capture the modules
	Dependencies         []Dependency         `xml:"dependencies>dependency"`
	DependencyManagement DependencyManagement `xml:"dependencyManagement"`
	Build                Build                `xml:"build"`
}

// Parent represents the parent POM if this project is a module.
type Parent struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
}

// Dependency represents a single Maven dependency.
type Dependency struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope,omitempty"`
}

// DependencyManagement includes a list of dependencies that are managed.
type DependencyManagement struct {
	Dependencies []Dependency `xml:"dependencies>dependency"`
}

// Build represents the build configuration which can contain plugins.
type Build struct {
	Plugins []Plugin `xml:"plugins>plugin"`
}

// Plugin represents a build plugin.
type Plugin struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
	//Configuration xml.Node `xml:"configuration"`
}

// ParsePOM Parse the POM file.
func ParsePOM(filePath string) (*MavenProject, error) {
	xmlFile, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer xmlFile.Close()

	bytes, err := ioutil.ReadAll(xmlFile)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	var project MavenProject
	if err := xml.Unmarshal(bytes, &project); err != nil {
		return nil, fmt.Errorf("error parsing XML: %w", err)
	}

	return &project, nil
}
