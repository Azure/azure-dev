// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const ProjectFileName = "azure.yaml"
const EnvironmentDirectoryName = ".azure"
const DotEnvFileName = ".env"
const ConfigFileName = "config.json"
const ConfigFileVersion = 1

// ProjectFileNames lists all valid project file names, in order of preference
var ProjectFileNames = []string{"azure.yaml", "azure.yml"}

type AzdContext struct {
	projectDirectory string
	projectFilePath  string
}

func (c *AzdContext) ProjectDirectory() string {
	return c.projectDirectory
}

func (c *AzdContext) SetProjectDirectory(dir string) {
	c.projectDirectory = dir
}

// ProjectPath returns the path to the project file. If the context was created by searching
// for a project file, returns the actual file that was found. Otherwise, returns the default
// project file name joined with the project directory (useful when creating new projects).
func (c *AzdContext) ProjectPath() string {
	if c.projectFilePath != "" {
		return c.projectFilePath
	}
	return filepath.Join(c.ProjectDirectory(), ProjectFileName)
}

func (c *AzdContext) EnvironmentDirectory() string {
	return filepath.Join(c.ProjectDirectory(), EnvironmentDirectoryName)
}

// ProjectName returns a suitable project name from the given project directory.
func ProjectName(projectDirectory string) string {
	return names.LabelName(filepath.Base(projectDirectory))
}

func (c *AzdContext) EnvironmentRoot(name string) string {
	return filepath.Join(c.EnvironmentDirectory(), name)
}

func (c *AzdContext) GetEnvironmentWorkDirectory(name string) string {
	return filepath.Join(c.EnvironmentRoot(name), "wd")
}

// GetDefaultEnvironmentName returns the name of the default environment. Returns
// an empty string if a default environment has not been set.
func (c *AzdContext) GetDefaultEnvironmentName() (string, error) {
	path := filepath.Join(c.EnvironmentDirectory(), ConfigFileName)
	file, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("reading config file: %w", err)
	}

	var config configFile
	if err := json.Unmarshal(file, &config); err != nil {
		return "", fmt.Errorf("deserializing config file: %w", err)
	}

	return config.DefaultEnvironment, nil
}

// ProjectState represents the state of the project.
type ProjectState struct {
	DefaultEnvironment string
}

// SetProjectState persists the state of the project to the file system, like the default environment.
func (c *AzdContext) SetProjectState(state ProjectState) error {
	path := filepath.Join(c.EnvironmentDirectory(), ConfigFileName)
	config := configFile{
		Version:            ConfigFileVersion,
		DefaultEnvironment: state.DefaultEnvironment,
	}

	if err := writeConfig(path, config); err != nil {
		return err
	}

	// make sure to ignore the environment directory
	path = filepath.Join(c.EnvironmentDirectory(), ".gitignore")
	return os.WriteFile(path, []byte("# .azure is not intended to be committed\n*"), osutil.PermissionFile)
}

// Creates context with project directory set to the desired directory.
func NewAzdContextWithDirectory(projectDirectory string) *AzdContext {
	return &AzdContext{
		projectDirectory: projectDirectory,
	}
}

var (
	ErrNoProject = errors.New("no project exists; to create a new project, run `azd init`")
)

// Creates context with project directory set to the nearest project file found by calling NewAzdContextFromWd
// on the current working directory.
func NewAzdContext() (*AzdContext, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get the current directory: %w", err)
	}

	return NewAzdContextFromWd(wd)
}

// Creates context with project directory set to the nearest project file found.
//
// The project file is first searched for in the working directory, if not found, the parent directory is searched
// recursively up to root. If no project file is found, errNoProject is returned.
func NewAzdContextFromWd(wd string) (*AzdContext, error) {
	// Walk up from the wd to the root, looking for a project file. If we find one, that's
	// the root project directory.
	searchDir, err := filepath.Abs(wd)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	var foundProjectFilePath string
	for {
		// Try all valid project file names in order of preference
		for _, fileName := range ProjectFileNames {
			projectFilePath := filepath.Join(searchDir, fileName)
			stat, err := os.Stat(projectFilePath)
			if err == nil && !stat.IsDir() {
				// We found a valid project file
				foundProjectFilePath = projectFilePath
				break
			}
		}

		if foundProjectFilePath != "" {
			// We found our project file, and searchDir is the directory that contains it.
			break
		}

		// No project file found in this directory, move up to parent
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			return nil, ErrNoProject
		}
		searchDir = parent
	}

	return &AzdContext{
		projectDirectory: searchDir,
		projectFilePath:  foundProjectFilePath,
	}, nil
}

type configFile struct {
	Version            int    `json:"version"`
	DefaultEnvironment string `json:"defaultEnvironment,omitempty"`
}

func writeConfig(path string, config configFile) error {
	bytes, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("serializing config file: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating environment root: %w", err)
	}

	if err := os.WriteFile(path, bytes, osutil.PermissionFile); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}
