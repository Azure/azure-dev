package azdcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const ProjectFileName = "azure.yaml"
const EnvironmentDirectoryName = ".azure"
const DotEnvFileName = ".env"
const ConfigFileName = "config.json"
const ConfigFileVersion = 1
const InfraDirectoryName = "infra"

type AzdContext struct {
	projectDirectory string
}

func (c *AzdContext) ProjectDirectory() string {
	return c.projectDirectory
}

func (c *AzdContext) SetProjectDirectory(dir string) {
	c.projectDirectory = dir
}

func (c *AzdContext) ProjectPath() string {
	return filepath.Join(c.ProjectDirectory(), ProjectFileName)
}

func (c *AzdContext) EnvironmentDirectory() string {
	return filepath.Join(c.ProjectDirectory(), EnvironmentDirectoryName)
}

func (c *AzdContext) InfrastructureDirectory() string {
	return filepath.Join(c.ProjectDirectory(), InfraDirectoryName)
}

func (c *AzdContext) GetDefaultProjectName() string {
	return filepath.Base(c.ProjectDirectory())
}

func (c *AzdContext) EnvironmentDotEnvPath(name string) string {
	return filepath.Join(c.EnvironmentDirectory(), name, DotEnvFileName)
}

func (c *AzdContext) EnvironmentRoot(name string) string {
	return filepath.Join(c.EnvironmentDirectory(), name)
}

func (c *AzdContext) GetEnvironmentWorkDirectory(name string) string {
	return filepath.Join(c.EnvironmentRoot(name), "wd")
}

func (c *AzdContext) GetInfrastructurePath() string {
	return filepath.Join(c.ProjectDirectory(), InfraDirectoryName)
}

func (c *AzdContext) ListEnvironments() ([]contracts.EnvListEnvironment, error) {
	defaultEnv, err := c.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	ents, err := os.ReadDir(c.EnvironmentDirectory())
	if err != nil {
		return nil, fmt.Errorf("listing entries: %w", err)
	}

	var envs []contracts.EnvListEnvironment
	for _, ent := range ents {
		if ent.IsDir() {
			ev := contracts.EnvListEnvironment{
				Name:       ent.Name(),
				IsDefault:  ent.Name() == defaultEnv,
				DotEnvPath: c.EnvironmentDotEnvPath(ent.Name()),
			}
			envs = append(envs, ev)
		}
	}

	sort.Slice(envs, func(i, j int) bool {
		return envs[i].Name < envs[j].Name
	})
	return envs, nil
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

func (c *AzdContext) SetDefaultEnvironmentName(name string) error {
	path := filepath.Join(c.EnvironmentDirectory(), ConfigFileName)
	bytes, err := json.Marshal(configFile{
		Version:            ConfigFileVersion,
		DefaultEnvironment: name,
	})
	if err != nil {
		return fmt.Errorf("serializing config file: %w", err)
	}

	if err := os.WriteFile(path, bytes, osutil.PermissionFile); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

var ErrEnvironmentExists = errors.New("environment already exists")

func (c *AzdContext) NewEnvironment(name string) error {
	if err := os.MkdirAll(c.EnvironmentDirectory(), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating environment root: %w", err)
	}

	if err := os.Mkdir(filepath.Join(c.EnvironmentDirectory(), name), osutil.PermissionDirectory); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrEnvironmentExists
		}

		return fmt.Errorf("creating environment directory: %w", err)
	}

	return nil
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

// Creates context with project directory set to the nearest project file found.
//
// The project file is first searched for in the current directory, if not found, the parent directory is searched
// recursively up to root. If no project file is found, errNoProject is returned.
func NewAzdContext() (*AzdContext, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get the current directory: %w", err)
	}

	// Walk up from the CWD to the root, looking for a project file. If we find one, that's
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
	Version            int    `json:"version"`
	DefaultEnvironment string `json:"defaultEnvironment"`
}
