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
const ConfigFileVersion = 2

type AzdContext struct {
	projectDirectory string
}

func (c *AzdContext) ProjectFileName() string {
	return c.ProjectFileNameForEnvironment("")
}

// ProjectFileNameForEnvironment returns the project file name for a specific environment.
// If environmentName is empty, uses the default environment.
func (c *AzdContext) ProjectFileNameForEnvironment(environmentName string) string {
	fmt.Printf("[DEBUG] ProjectFileNameForEnvironment called with environmentName: '%s'\n", environmentName)

	// Get environment type for the specified environment (or default if empty)
	envType, err := c.GetEnvironmentType(environmentName)
	if err != nil {
		fmt.Printf("[DEBUG] Error getting environment type for '%s': %v, falling back to 'azure.yaml'\n", environmentName, err)
		return "azure.yaml"
	}

	if envType == "" {
		return "azure.yaml"
	}

	projectFileName := fmt.Sprintf("azure.%s.yaml", envType)
	fmt.Printf("[DEBUG] Project file name resolved to: '%s'\n", projectFileName)
	return projectFileName
}

func (c *AzdContext) ProjectDirectory() string {
	return c.projectDirectory
}

func (c *AzdContext) SetProjectDirectory(dir string) {
	c.projectDirectory = dir
}

func (c *AzdContext) ProjectPath() string {
	fileName := c.ProjectFileName()
	projectPath := filepath.Join(c.ProjectDirectory(), fileName)
	fmt.Printf("[DEBUG] ProjectPath resolved to: '%s'\n", projectPath)
	return projectPath
}

// ProjectPathForEnvironment returns the project path for a specific environment.
// If environmentName is empty, uses the default environment.
func (c *AzdContext) ProjectPathForEnvironment(environmentName string) string {
	fileName := c.ProjectFileNameForEnvironment(environmentName)
	projectPath := filepath.Join(c.ProjectDirectory(), fileName)
	fmt.Printf("[DEBUG] ProjectPathForEnvironment resolved to: '%s'\n", projectPath)
	return projectPath
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

	fmt.Printf("[DEBUG] Config file parsed, defaultEnvironment: '%s'\n", config.DefaultEnvironment)
	return config.DefaultEnvironment, nil
}

// GetEnvironmentTypes returns the defined environment types for the project.
func (c *AzdContext) GetEnvironmentTypes() ([]string, error) {
	path := filepath.Join(c.EnvironmentDirectory(), ConfigFileName)
	file, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var config configFile
	if err := json.Unmarshal(file, &config); err != nil {
		return nil, fmt.Errorf("deserializing config file: %w", err)
	}

	return config.EnvironmentTypes, nil
}

// GetEnvironmentType returns the environment type for a specific environment.
// If environmentName is empty, returns the type for the default environment.
func (c *AzdContext) GetEnvironmentType(environmentName string) (string, error) {
	var targetEnvName string

	if environmentName == "" {
		fmt.Println("[DEBUG] No environment name provided, getting default environment name")
		defaultEnvName, err := c.GetDefaultEnvironmentName()
		if err != nil {
			return "", fmt.Errorf("getting default environment name: %w", err)
		}

		fmt.Printf("[DEBUG] Default environment name: '%s'\n", defaultEnvName)

		if defaultEnvName == "" {
			fmt.Println("[DEBUG] No default environment name found")
			return "", nil
		}

		targetEnvName = defaultEnvName
	} else {
		targetEnvName = environmentName
	}

	// Read the environment's config.json file
	envConfigPath := filepath.Join(c.EnvironmentRoot(targetEnvName), ConfigFileName)
	fmt.Printf("[DEBUG] Reading environment config from: '%s'\n", envConfigPath)

	file, err := os.ReadFile(envConfigPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("reading environment config file: %w", err)
	}

	// Parse the JSON to extract environmentType
	var envConfig map[string]interface{}
	if err := json.Unmarshal(file, &envConfig); err != nil {
		return "", fmt.Errorf("deserializing environment config file: %w", err)
	}

	fmt.Printf("[DEBUG] Environment config parsed: %+v\n", envConfig)

	if envType, exists := envConfig["environmentType"]; exists {
		if envTypeStr, ok := envType.(string); ok {
			fmt.Printf("[DEBUG] Found environment type: '%s'\n", envTypeStr)
			return envTypeStr, nil
		}
		fmt.Printf("[DEBUG] Environment type exists but is not a string: %v\n", envType)
	} else {
		fmt.Println("[DEBUG] No 'environmentType' field found in config")
	}

	return "", nil
}

// ProjectState represents the state of the project.
type ProjectState struct {
	DefaultEnvironment string
	EnvironmentTypes   []string
}

// SetProjectState persists the state of the project to the file system, like the default environment.
func (c *AzdContext) SetProjectState(state ProjectState) error {
	path := filepath.Join(c.EnvironmentDirectory(), ConfigFileName)

	// Use provided environment types or fallback to defaults
	envTypes := state.EnvironmentTypes
	if len(envTypes) == 0 {
		envTypes = []string{"dev", "prod"}
	}

	config := configFile{
		Version:            ConfigFileVersion,
		DefaultEnvironment: state.DefaultEnvironment,
		EnvironmentTypes:   envTypes,
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
		projectDirectory: searchDir,
	}, nil
}

type configFile struct {
	Version            int      `json:"version"`
	DefaultEnvironment string   `json:"defaultEnvironment,omitempty"`
	EnvironmentTypes   []string `json:"environmentTypes,omitempty"`
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
