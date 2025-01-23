package appdetect

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

func toPom(ctx context.Context, mvnCli *maven.Cli, pomFilePath string) (pom, error) {
	result, err := toEffectivePom(ctx, mvnCli, pomFilePath)
	if err == nil {
		result.path = filepath.Dir(pomFilePath)
		return result, nil
	}

	result, err = unmarshalPomFile(pomFilePath)
	if err == nil {
		result.path = filepath.Dir(pomFilePath)
		return result, nil
	}
	return pom{}, err
}

func toEffectivePom(ctx context.Context, mvnCli *maven.Cli, pomFilePath string) (pom, error) {
	effectivePom, err := mvnCli.EffectivePom(ctx, pomFilePath)
	if err != nil {
		return pom{}, err
	}
	var resultPom pom
	err = xml.Unmarshal([]byte(effectivePom), &resultPom)
	return resultPom, err
}

func unmarshalPomFile(pomFilePath string) (pom, error) {
	bytes, err := os.ReadFile(pomFilePath)
	if err != nil {
		return pom{}, err
	}

	var result pom
	if err := xml.Unmarshal(bytes, &result); err != nil {
		return pom{}, fmt.Errorf("parsing xml: %w", err)
	}

	return result, nil
}

// pom represents the top-level structure of a Maven POM file.
type pom struct {
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
