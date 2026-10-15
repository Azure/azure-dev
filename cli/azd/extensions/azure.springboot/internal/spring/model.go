// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

// ModelResolver resolves a trusted application's effective Maven model.
type ModelResolver interface {
	Resolve(context.Context, string) ([]byte, error)
}

type mavenResolver struct {
	writer io.Writer
}

// NewModelResolver uses azd's Maven wrapper and effective-model implementation.
func NewModelResolver(writer io.Writer) ModelResolver {
	return &mavenResolver{writer: writer}
}

func (r *mavenResolver) Resolve(ctx context.Context, root string) ([]byte, error) {
	runner := exec.NewCommandRunner(&exec.RunnerOptions{Stdout: r.writer, Stderr: r.writer})
	cli := maven.NewCli(runner)
	cli.SetPath(root, root)
	model, err := cli.EffectivePom(ctx, filepath.Join(root, "pom.xml"))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &modelError{cause: err}
	}
	return []byte(model), nil
}

// Maven errors can contain repository URLs, credentials, or effective-model output.
type modelError struct {
	cause error
}

func (e *modelError) Error() string {
	return "Maven model resolution failed; run Maven help:effective-pom in the trusted project to diagnose"
}

func (e *modelError) Unwrap() error { return e.cause }

type coordinate struct {
	Group    string `xml:"groupId"`
	Artifact string `xml:"artifactId"`
	Scope    string `xml:"scope"`
}

type buildPlugin struct {
	Group      string `xml:"groupId"`
	Artifact   string `xml:"artifactId"`
	Executions []struct {
		Phase string   `xml:"phase"`
		Goals []string `xml:"goals>goal"`
	} `xml:"executions>execution"`
	Configuration struct {
		Skip       string `xml:"skip"`
		Classifier string `xml:"classifier"`
		Layout     string `xml:"layout"`
	} `xml:"configuration"`
}

type model struct {
	Artifact     string       `xml:"artifactId"`
	Version      string       `xml:"version"`
	Packaging    string       `xml:"packaging"`
	Modules      []string     `xml:"modules>module"`
	Dependencies []coordinate `xml:"dependencies>dependency"`
	Properties   struct {
		JavaVersion string `xml:"java.version"`
		Release     string `xml:"maven.compiler.release"`
		Target      string `xml:"maven.compiler.target"`
	} `xml:"properties"`
	Build struct {
		FinalName string        `xml:"finalName"`
		Plugins   []buildPlugin `xml:"plugins>plugin"`
	} `xml:"build"`
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func parseModel(data []byte) (*model, int, error) {
	var m model
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil, 0, errors.New("Maven returned an invalid effective model")
	}
	if len(m.Modules) != 0 || (m.Packaging != "" && m.Packaging != "jar") {
		return nil, 0, errors.New("initialization currently requires a single-module, executable-JAR application")
	}
	boot := false
	for _, plugin := range m.Build.Plugins {
		if plugin.Group != "org.springframework.boot" || plugin.Artifact != "spring-boot-maven-plugin" {
			continue
		}
		if plugin.Configuration.Skip != "" && plugin.Configuration.Skip != "false" ||
			plugin.Configuration.Classifier != "" || plugin.Configuration.Layout == "NONE" {
			return nil, 0, errors.New("custom or disabled Spring Boot JAR repackaging is not yet supported")
		}
		for _, execution := range plugin.Executions {
			if (execution.Phase == "" || execution.Phase == "package") && slices.Contains(execution.Goals, "repackage") {
				boot = true
			}
		}
	}
	if !boot {
		return nil, 0, errors.New("an active Spring Boot repackage execution in the Maven package phase is required")
	}
	if !safeName.MatchString(m.Artifact) {
		return nil, 0, errors.New("Maven artifactId must be a safe file name")
	}
	if m.Build.FinalName == "" {
		m.Build.FinalName = m.Artifact + "-" + m.Version
	}
	if !safeName.MatchString(m.Build.FinalName) {
		return nil, 0, errors.New("Maven finalName must be a safe file name")
	}
	version := m.Properties.Release
	if version == "" {
		version = m.Properties.JavaVersion
	}
	if version == "" {
		version = m.Properties.Target
	}
	java, err := strconv.Atoi(version)
	if err != nil || java < 17 {
		return nil, 0, errors.New("a Java release of 17 or newer must be specified in the effective Maven model")
	}
	return &m, java, nil
}

func (m *model) hasDependency(group, artifact string) bool {
	for _, d := range m.Dependencies {
		if d.Group == group && d.Artifact == artifact &&
			(d.Scope == "" || d.Scope == "compile" || d.Scope == "runtime" || d.Scope == "provided") {
			return true
		}
	}
	return false
}

func (m *model) hasPackagedIdentityExtensions() bool {
	for _, d := range m.Dependencies {
		if d.Group == "com.azure" && d.Artifact == "azure-identity-extensions" &&
			(d.Scope == "" || d.Scope == "compile" || d.Scope == "runtime") {
			return true
		}
	}
	return false
}

func projectName(artifact string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(artifact) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
