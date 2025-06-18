// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/joho/godotenv"
)

const ProjectFileName = "azure.yaml"
const EnvironmentDirectoryName = ".azure"
const DotEnvFileName = ".env"
const ConfigFileName = "config.json"
const ConfigFileVersion = 1

// Environment types should only contain alphanumeric characters
var environmentTypeRegexp = regexp.MustCompile(`^[a-zA-Z0-9]{1,32}$`)

type AzdContext struct {
	projectDirectory string
}

func (c *AzdContext) ProjectFileName() string {
	return c.ProjectFileNameForEnvironment("")
}

// ProjectFileNameForEnvironment returns the project file name for a specific environment.
// If environmentName is empty, uses the default environment.
func (c *AzdContext) ProjectFileNameForEnvironment(environmentName string) string {
	// Get environment type for the specified environment (or default if empty)
	envType, err := c.GetEnvironmentType(environmentName)
	if err != nil {
		log.Printf("getting env type: %v", err)
		envType = ""
	}

	return ProjectFileNameForEnvironmentType(envType)
}

// ProjectFileNameForEnvironmentType returns the project file name for a specific environment type.
// If environmentType is empty, returns the default project file name.
func ProjectFileNameForEnvironmentType(environmentType string) string {
	if environmentType == "" {
		return "azure.yaml"
	}

	projectFileName := fmt.Sprintf("azure.%s.yaml", environmentType)
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
	return projectPath
}

// ProjectPathForEnvironment returns the project path for a specific environment.
// If environmentName is empty, uses the default environment.
func (c *AzdContext) ProjectPathForEnvironment(environmentName string) string {
	fileName := c.ProjectFileNameForEnvironment(environmentName)
	projectPath := filepath.Join(c.ProjectDirectory(), fileName)
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

// GetEnvironmentType returns the environment type for a specific environment.
// If environmentName is empty, returns the type for the default environment.
func (c *AzdContext) GetEnvironmentType(environmentName string) (string, error) {
	targetEnvName := environmentName

	if environmentName == "" {
		defaultEnvName, err := c.GetDefaultEnvironmentName()
		if err != nil {
			return "", fmt.Errorf("getting default environment name: %w", err)
		}

		if defaultEnvName == "" {
			return "", nil
		}

		targetEnvName = defaultEnvName
	}

	// Read the environment's .env file
	envFilePath := filepath.Join(c.EnvironmentRoot(targetEnvName), DotEnvFileName)
	envVars, err := godotenv.Read(envFilePath)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("reading environment .env file: %w", err)
	}

	if envType, exists := envVars["AZURE_ENV_TYPE"]; exists {
		return envType, nil
	}

	return "", nil
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

// hasValidProjectFile checks if a valid project file exists in the given directory.
// It looks for azure.yaml first, then checks for azure.{envType}.yaml files where envType is valid.
// Returns true if a valid project file is found, false otherwise.
func hasValidProjectFile(searchDir string) bool {
	// First check for the default azure.yaml (matching original logic exactly)
	defaultProjectPath := filepath.Join(searchDir, ProjectFileName)
	stat, err := os.Stat(defaultProjectPath)
	if err == nil && !stat.IsDir() {
		// Found azure.yaml and it's a file
		return true
	}
	if err != nil && !os.IsNotExist(err) {
		return false
	}

	// Then check for azure.{envType}.yaml files
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		// Check if filename matches azure.{envType}.yaml pattern
		if strings.HasPrefix(filename, "azure.") && strings.HasSuffix(filename, ".yaml") {
			envType := strings.TrimPrefix(filename, "azure.")
			envType = strings.TrimSuffix(envType, ".yaml")

			if envType != "" && environmentTypeRegexp.MatchString(envType) {
				return true
			}
		}
	}

	return false
}

// Creates context with project directory set to the nearest project file found.
//
// The project file is first searched for in the working directory, if not found, the parent directory is searched
// recursively up to root. The search looks for azure.yaml first, then for azure.{envType}.yaml files where envType
// is a valid environment type. If no project file is found, errNoProject is returned.
func NewAzdContextFromWd(wd string) (*AzdContext, error) {
	// Walk up from the wd to the root, looking for a project file. If we find one, that's
	// the root project directory.
	searchDir, err := filepath.Abs(wd)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	for {
		if hasValidProjectFile(searchDir) {
			return &AzdContext{
				projectDirectory: searchDir,
			}, nil
		}

		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			return nil, ErrNoProject
		}
		searchDir = parent
	}
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
