// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"gopkg.in/yaml.v3"
)

const (
	// TODO: update this to point directly to schema file on GitHub after public preview  for AZD ships.
	projectSchemaAnnotation = "# yaml-language-server: $schema=https://azuresdkreleasepreview.blob.core.windows.net/azd/schema/azure.yaml.json"
)

type Project struct {
	Name     string
	Config   *ProjectConfig
	Metadata *ProjectMetadata
	Services []*Service
}

// ReadProject reads a project file and sets the configured template
// to include the template name in `metadata/template` from the yaml in projectPath.
func ReadProject(ctx context.Context, projectPath string, env *environment.Environment) (*Project, error) {
	projectRootDir := filepath.Dir(projectPath)

	// Load Project configuration
	projectConfig, err := LoadProjectConfig(projectRootDir, env)
	if err != nil {
		return nil, fmt.Errorf("reading project config: %w", err)
	}

	// Evaluate project
	project, err := projectConfig.GetProject(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("reading project: %w", err)
	}

	return project, nil
}

func NewProject(path string, name string) (*Project, error) {
	projectBytes, err := yaml.Marshal(ProjectConfig{
		Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling project file to yaml: %w", err)
	}

	newLine := osutil.GetNewLineSeparator()
	projectFileContents := bytes.NewBufferString(projectSchemaAnnotation + newLine + newLine)
	_, err = projectFileContents.Write(projectBytes)
	if err != nil {
		return nil, fmt.Errorf("preparing new project file contents: %w", err)
	}

	err = os.WriteFile(path, projectFileContents.Bytes(), 0644)
	if err != nil {
		return nil, fmt.Errorf("writing project file: %w", err)
	}

	return &Project{
		Name:     name,
		Services: make([]*Service, 0),
	}, nil
}
