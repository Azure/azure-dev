package azdcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// ProjectFileName is the name of the file that stores the project configuration. This file is located in the root of the
// and contains the project name and other project specific configuration.
const ProjectFileName = "azure.yaml"

// EnvironmentConfigDirectoryName is the name of the directory that contains environment specific configuration.
// This directory is located in the root of the azd project and is not intended to be committed. Inside this directory
// is a folder for each environment and a config.json in the root file that stores the default environment.
const EnvironmentConfigDirectoryName = ".azure"

// configFileName is the name of the file that stores the default environment and is located in the root of the .azure
// directory.
const configFileName = "config.json"

// configFileVersion is the version of the config file format that we understand and write.
const configFileVersion = 1

// AzdContext is wrapper around the root of an azd project on a file system. It is the directory that contains the azure.yaml
// and .azure folder.
type AzdContext struct {
	rootDirectory string
}

// RootDirectory is the path to the root of the azd project (i.e. the folder that contains .azure and azure.yaml).
func (c *AzdContext) RootDirectory() string {
	return c.rootDirectory
}

// EnvironmentConfigPath returns the path to the .azure directory in the root of the azd project.
func EnvironmentConfigPath(c *AzdContext) string {
	return filepath.Join(c.RootDirectory(), EnvironmentConfigDirectoryName)
}

// ProjectPath returns the path to the azure.yaml file in the root of the azd project.
func ProjectPath(c *AzdContext) string {
	return filepath.Join(c.RootDirectory(), ProjectFileName)
}

// DefaultEnvironmentName returns the name of the default environment or an empty string if no default environment is set.
func (c *AzdContext) DefaultEnvironmentName() (string, error) {
	path := filepath.Join(EnvironmentConfigPath(c), configFileName)
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

// SetDefaultEnvironmentName saves the environment that is used by default when azd is run without a `-e` flag. Using "" as
// the name will cause azd to prompt the user to select an environment in the future.
func (c *AzdContext) SetDefaultEnvironmentName(name string) error {
	path := filepath.Join(EnvironmentConfigPath(c), configFileName)
	config := configFile{
		Version:            configFileVersion,
		DefaultEnvironment: name,
	}

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

	// make sure to ignore the environment directory
	path = filepath.Join(EnvironmentConfigPath(c), ".gitignore")
	return os.WriteFile(path, []byte("# .azure is not intended to be committed\n*"), osutil.PermissionFile)
}

// Creates context with project directory set to the desired directory.
func NewAzdContextWithDirectory(projectDirectory string) *AzdContext {
	return &AzdContext{
		rootDirectory: projectDirectory,
	}
}

var (
	// ErrNoProject is returned by NewAzdContextFromWd when no project file is found.
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
// recursively up to root. If no project file is found, an error that matches [ErrNoProject] with [errors.Is] is returned.
func NewAzdContextFromWd(wd string) (*AzdContext, error) {
	// Walk up from the wd to the root, looking for a project file. If we find one, that's
	// the root project directory.
	searchDir, err := filepath.Abs(wd)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	for {
		projectFilePath := filepath.Join(searchDir, ProjectFileName)
		stat, err := os.Stat(projectFilePath)
		if os.IsNotExist(err) || (err == nil && stat.IsDir()) {
			parent := filepath.Dir(searchDir)
			if parent == searchDir {
				return nil, ErrNoProject
			}
			searchDir = parent
		} else if err == nil {
			// We found our azure.yaml file, and searchDir is the directory
			// that contains it.
			break
		} else {
			return nil, fmt.Errorf("searching for project file: %w", err)
		}
	}

	return &AzdContext{
		rootDirectory: searchDir,
	}, nil
}

// configFile is the model type for the config file that is stored in the root of the .azure directory. It can be read and
// written using the json.Marshal and json.Unmarshal functions.
type configFile struct {
	Version            int    `json:"version"`
	DefaultEnvironment string `json:"defaultEnvironment,omitempty"`
}
